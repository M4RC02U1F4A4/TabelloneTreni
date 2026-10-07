package statolinee

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/trenord"
)

// Quanto si accetta da chi si abbona. Non sono numeri di capacità: l'endpoint
// è raggiungibile da chiunque apra l'applicazione, e senza un tetto una sola
// richiesta ripetuta riempirebbe il disco.
const (
	MaxAbbonamenti         = 500
	MaxLineePerAbbonamento = 100
	// I treni si seguono a mano, uno alla volta, e si spengono da sé quando il
	// viaggio finisce: chi ne ha venti aperti insieme non sta aspettando un
	// treno, sta facendo altro.
	MaxTreniPerAbbonamento = 20
)

// Abbonamento è un telefono che vuole essere avvisato: su certe linee, su
// certi treni, o su entrambe le cose.
type Abbonamento struct {
	Sottoscrizione webpush.Subscription `json:"subscription"`
	Linee          []string             `json:"lines"`

	// I treni che questa persona sta aspettando adesso. Sono l'opposto delle
	// linee sotto ogni aspetto: le linee si seguono per mesi e vanno filtrate
	// con le fasce, un treno lo si segue per due ore e poi non esiste più —
	// tanto che questo elenco lo pota il servizio stesso, quando il treno
	// arriva dove la persona sale.
	Treni []TrenoSeguito `json:"trains,omitempty"`

	// I treni che questa persona prende sempre. A differenza dei segnalibri
	// non li pota nessuno: ogni giorno scelto ne nasce il treno di oggi, che
	// vive dal preavviso all'arrivo e il giorno dopo ricomincia.
	Abituali []TrenoAbituale `json:"habitual,omitempty"`

	// Le finestre in cui questa persona vuole essere avvisata, e il quadrante
	// su cui leggerne gli orari. Vuote: sempre, che è come stavano le cose
	// prima che le fasce esistessero.
	Fasce []Fascia `json:"windows,omitempty"`
	Zona  string   `json:"timezone,omitempty"`

	// Visto è quello che a questo abbonato è già stato raccontato, linea per
	// linea. Non arriva dal client: è contabilità del servizio.
	//
	// Sta per abbonato e non una volta per tutti perché con le fasce due
	// persone non sono più allo stesso punto della storia: chi ascolta dalle 7
	// alle 9 e chi ascolta dalle 17 alle 19 hanno sentito cose diverse, e un
	// "ultimo stato noto" solo non può rispondere a entrambi. È anche ciò che
	// rende possibile la regola che conta: un guasto comparso alle 6 e ancora
	// lì alle 7 viene raccontato alle 7, perché alle 7 è ancora diverso da
	// quello che l'abbonato sa — mentre uno comparso e rientrato entro le 7
	// non lo è più, e giustamente non arriva.
	Visto map[string]Visto `json:"seen,omitempty"`

	// Lo stesso, per i treni, indicizzato sulla chiave del treno. È una mappa
	// a parte e non la stessa di sopra perché le chiavi sono di due generi
	// diversi — "S2" e "S01700|2247|1788645600000" — e mescolarle sarebbe una
	// collisione in attesa di un codice di linea sfortunato.
	VistoTreni map[string]VistoTreno `json:"seenTrains,omitempty"`
}

// TrenoSeguito è un treno che qualcuno sta aspettando, con la stazione a cui
// lo aspetta.
//
// Le prime tre sono le coordinate con cui ViaggiaTreno identifica un viaggio, e
// sono le stesse che il telefono si è salvato mettendo il segnalibro. La
// quarta è l'unica cosa che riguarda chi guarda e non il treno: la stazione da
// cui sale, che è dove le notifiche devono smettere.
type TrenoSeguito struct {
	Origine string `json:"origin"`
	Numero  string `json:"number"`
	Data    int64  `json:"date"`
	Da      int    `json:"from"`
}

// Chiave identifica il viaggio, non chi lo segue: due persone che aspettano lo
// stesso treno a due stazioni diverse hanno la stessa chiave, ed è quello che
// permette di leggerlo una volta sola per entrambe.
func (t TrenoSeguito) Chiave() string {
	return t.Origine + "|" + t.Numero + "|" + strconv.FormatInt(t.Data, 10)
}

// VistoTreno è quello che a questo abbonato è già stato raccontato di questo
// treno.
//
// Rilevamento da solo basterebbe per la regola richiesta — una notifica a ogni
// rilevamento di ViaggiaTreno — ma gli altri tre sono le cose che cambiano
// senza che l'orologio si muova: un binario assegnato mezz'ora prima, una
// soppressione pubblicata su un treno fermo. Tacerle sarebbe il contrario di
// "tutti gli aggiornamenti".
type VistoTreno struct {
	Rilevamento   int64 `json:"detected,omitempty"`
	Ritardo       int   `json:"delay,omitempty"`
	Provvedimento bool  `json:"disrupted,omitempty"`
	// Il binario alla fermata di salita di *questo* abbonato, che è il motivo
	// per cui questa memoria sta sull'abbonamento e non sul treno.
	Binario string `json:"platform,omitempty"`

	// Preavviso dice che la notifica prima della partenza è già partita.
	Preavviso bool `json:"warned,omitempty"`
	// Finito è il treno di oggi di un abituale già arrivato dove si sale. Un
	// segnalibro finito esce dall'elenco e basta; il treno di un abituale
	// invece rinascerebbe al giro dopo come nuovo, e serve ricordarsi che per
	// oggi è chiuso.
	Finito bool `json:"done,omitempty"`
}

// Visto è lo stato di una linea come lo si è raccontato per ultimo a qualcuno.
type Visto struct {
	Stato trenord.Stato `json:"state"`
	// Le impronte delle comunicazioni già mandate. Impronte e non testi: sono
	// paragrafi, e questo file si riscrive per intero a ogni notifica.
	Avvisi []string `json:"notices,omitempty"`
}

// impronta riconosce una comunicazione dal testo, che è l'unica cosa stabile
// che abbia: Trenord ripubblica lo stesso avviso con l'ora aggiornata quando lo
// ritocca, e la data lo farebbe sembrare nuovo ogni volta.
func impronta(testo string) string {
	h := fnv.New64a()
	h.Write([]byte(testo))
	return strconv.FormatUint(h.Sum64(), 36)
}

// breve è come un abbonamento compare nel log: le ultime cifre del suo
// indirizzo, che bastano a distinguere due telefoni.
//
// L'indirizzo intero non ci va: è la chiave con cui si spedisce a quel
// telefono, e un log è il posto sbagliato dove tenerla.
func breve(endpoint string) string {
	if len(endpoint) <= 8 {
		return "…"
	}
	return "…" + endpoint[len(endpoint)-8:]
}

// fasceScritte è come le fasce compaiono nel log, per poterle confrontare con
// quelle che l'interfaccia mostra: è il solo modo di accorgersi che il
// telefono e il servizio non stanno dicendo la stessa cosa.
func (ab Abbonamento) fasceScritte() string {
	if len(ab.Fasce) == 0 {
		return "nessuna fascia: sempre"
	}
	fuso := "Europe/Rome per difetto"
	if ab.Zona != "" {
		fuso = ab.Zona
	}
	parti := make([]string, 0, len(ab.Fasce))
	for _, f := range ab.Fasce {
		g := "tutti i giorni"
		if len(f.Giorni) > 0 {
			n := make([]string, 0, len(f.Giorni))
			for _, x := range f.Giorni {
				n = append(n, strconv.Itoa(x))
			}
			g = "giorni " + strings.Join(n, ",")
		}
		parti = append(parti, fmt.Sprintf("%s-%s %s", f.Da, f.A, g))
	}
	return strings.Join(parti, "; ") + " — " + fuso
}

// InAscolto dice se adesso è un momento in cui questo abbonato vuole sapere le
// cose.
func (ab Abbonamento) InAscolto(adesso time.Time) bool {
	return dentro(ab.Fasce, adesso.In(ab.fuso()))
}

// fuso è il quadrante su cui leggere gli orari delle fasce: quello del telefono
// che le ha scritte. Senza, o con un nome che questo binario non conosce, vale
// l'ora italiana — l'app parla di treni lombardi, e l'ora del container non
// c'entra niente con quella di nessuno.
func (ab Abbonamento) fuso() *time.Location {
	if ab.Zona != "" {
		if l, err := time.LoadLocation(ab.Zona); err == nil {
			return l
		}
	}
	return roma
}

// roma non può mancare: il database dei fusi è dentro il binario, vedi
// l'import di time/tzdata in fasce.go. Si carica qui e non in un init perché
// le variabili dei test la usano, e vengono inizializzate prima degli init.
var roma = func() *time.Location {
	l, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		panic(err)
	}
	return l
}()

// Abbonati custodisce gli abbonamenti e li tiene su disco.
//
// Il formato è un file JSON riscritto per intero a ogni modifica: gli
// abbonamenti sono decine, si toccano quando qualcuno accende una campanella,
// e un database qui costerebbe più di quanto risolve. Quello che invece serve
// davvero è che la scrittura sia atomica — un file troncato a metà da un
// riavvio perderebbe tutti gli abbonati insieme.
type Abbonati struct {
	mu       sync.RWMutex
	percorso string // vuoto: si tiene tutto in memoria e basta
	m        map[string]Abbonamento
}

func ApriAbbonati(percorso string) (*Abbonati, error) {
	a := &Abbonati{percorso: percorso, m: map[string]Abbonamento{}}
	if percorso == "" {
		return a, nil
	}
	b, err := os.ReadFile(percorso)
	if errors.Is(err, os.ErrNotExist) {
		return a, nil // primo avvio
	}
	if err != nil {
		return nil, err
	}
	var elenco []Abbonamento
	if err := json.Unmarshal(b, &elenco); err != nil {
		return nil, fmt.Errorf("%s: %w", percorso, err)
	}
	for _, ab := range elenco {
		a.m[ab.Sottoscrizione.Endpoint] = ab
	}
	return a, nil
}

func (a *Abbonati) Quanti() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.m)
}

// Registra aggiunge o aggiorna un abbonamento. Niente linee, niente treni e
// niente abituali lo cancella: è lo stesso gesto visto dall'altra parte — chi spegne l'ultima
// campanella e non aspetta nessun treno non vuole più essere avvisato, e
// tenerne memoria non serve a nessuno.
//
// Devono essere vuote *entrambe*. Guardare le sole linee, com'era prima che i
// treni esistessero, cancellerebbe l'abbonamento di chi segue un treno senza
// avere nessuna campanella accesa — cioè il caso più comune di tutti.
func (a *Abbonati) Registra(ab Abbonamento) error {
	if err := valida(ab); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	if ab.vuoto() {
		delete(a.m, ab.Sottoscrizione.Endpoint)
	} else {
		precedente, gia := a.m[ab.Sottoscrizione.Endpoint]
		if !gia && len(a.m) >= MaxAbbonamenti {
			return errors.New("troppi abbonamenti")
		}
		// Il visto non si prende da chi si registra: è memoria del servizio, e
		// il client la sovrascriverebbe con quello che ha, cioè niente.
		// L'applicazione riallinea l'abbonamento a ogni avvio — azzerando qui,
		// ogni riapertura riporterebbe ogni linea al "primo giro", che è quello
		// che per regola non annuncia nulla: la notizia successiva si
		// perderebbe in silenzio, che è il modo peggiore di perderla.
		ab.Visto = pota(precedente.Visto, ab.Linee)
		ab.VistoTreni = pota(precedente.VistoTreni, ab.chiaviInAttesa(time.Now()))
		a.m[ab.Sottoscrizione.Endpoint] = ab
	}
	return a.scrivi()
}

// pota tiene la memoria delle sole linee ancora seguite, o dei soli treni
// ancora attesi.
//
// Per le linee: chi spegne una campanella e la riaccende un mese dopo
// ricomincia da capo, che è giusto: di quel mese non gli è stato raccontato
// niente, e riprendere il filo da dove era vorrebbe dire annunciargli un cambio
// vecchio di settimane.
//
// Per i treni vale lo stesso ragionamento e in più uno suo: un treno che non è
// più atteso è finito, e la sua memoria non tornerà mai utile. Tenerla vorrebbe
// dire far crescere il file per sempre. Atteso vuol dire segnalibro o treno di
// oggi di un abituale: quest'ultimo resta in memoria fino alla fine della sua
// finestra anche da arrivato, che è quello che gli impedisce di rinascere.
func pota[V any](visto map[string]V, chiavi []string) map[string]V {
	if len(visto) == 0 {
		return nil
	}
	out := make(map[string]V, len(chiavi))
	for _, k := range chiavi {
		if v, cera := visto[k]; cera {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// vuoto è l'abbonamento di chi non vuole più sapere niente: nessuna
// campanella, nessun treno da aspettare, nessun treno di tutti i giorni.
func (ab Abbonamento) vuoto() bool {
	return len(ab.Linee) == 0 && len(ab.Treni) == 0 && len(ab.Abituali) == 0
}

// Tutti restituisce gli abbonamenti, in copia: chi li scorre per notificare fa
// richieste di rete lente, e tenere il lock per tutto quel tempo bloccherebbe
// anche chi si sta solo abbonando.
func (a *Abbonati) Tutti() []Abbonamento {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.elenco()
}

// elenco va chiamata con il lock preso. Mai nil: su disco un elenco vuoto è
// "[]", non "null".
func (a *Abbonati) elenco() []Abbonamento {
	return slices.AppendSeq(make([]Abbonamento, 0, len(a.m)), maps.Values(a.m))
}

// SegnaVisto sostituisce la memoria di un abbonamento e la scrive.
//
// Non resuscita un abbonamento che nel frattempo è stato cancellato — il
// servizio push può averlo rifiutato proprio durante l'invio, e riscriverlo
// significherebbe tornare a spedire a un telefono che non c'è.
func (a *Abbonati) SegnaVisto(endpoint string, visto map[string]Visto) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ab, cera := a.m[endpoint]
	if !cera {
		return
	}
	ab.Visto = visto
	a.m[endpoint] = ab
	if err := a.scrivi(); err != nil {
		log.Printf("abbonamenti: scrittura su %s: %v", a.percorso, err)
	}
}

// SegnaVistoTreni scrive la memoria dei treni e nello stesso giro toglie
// dall'elenco quelli finiti — arrivati dove la persona sale, o arrivati e
// basta. La memoria si pota su quello che resta atteso adesso.
//
// Le due cose insieme e non in due metodi perché succedono insieme, a fine
// giro, e il file si riscrive per intero a ogni scrittura: separarle vorrebbe
// dire riscriverlo due volte al minuto per abbonato invece di una.
//
// Come SegnaVisto, non resuscita un abbonamento cancellato nel frattempo: il
// servizio push può averlo rifiutato proprio durante l'invio.
func (a *Abbonati) SegnaVistoTreni(endpoint string, visto map[string]VistoTreno, finiti []string, adesso time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ab, cera := a.m[endpoint]
	if !cera {
		return
	}
	if len(finiti) > 0 {
		ab.Treni = slices.DeleteFunc(slices.Clone(ab.Treni), func(t TrenoSeguito) bool {
			return slices.Contains(finiti, t.Chiave())
		})
	}
	// Chi non aspetta più nessun treno e non ha campanelle accese non ha più
	// motivo di stare qui: è la stessa regola di Registra, applicata quando a
	// svuotare l'elenco è il servizio invece della persona.
	if ab.vuoto() {
		delete(a.m, endpoint)
		if err := a.scrivi(); err != nil {
			log.Printf("abbonamenti: scrittura su %s: %v", a.percorso, err)
		}
		return
	}
	ab.VistoTreni = pota(visto, ab.chiaviInAttesa(adesso))
	a.m[endpoint] = ab
	if err := a.scrivi(); err != nil {
		log.Printf("abbonamenti: scrittura su %s: %v", a.percorso, err)
	}
}

// TreniSeguiti è l'unione dei treni che qualcuno sta aspettando adesso,
// deduplicata: i segnalibri e i treni di oggi degli abituali non ancora
// arrivati.
//
// È il punto di tutto il disegno: il costo verso ViaggiaTreno cresce con i
// treni e non con i telefoni, e venti persone sullo stesso regionale restano
// una lettura sola.
func (a *Abbonati) TreniSeguiti(adesso time.Time) []TrenoSeguito {
	a.mu.RLock()
	defer a.mu.RUnlock()
	visti := map[string]bool{}
	var out []TrenoSeguito
	for _, ab := range a.m {
		for _, t := range ab.inAttesa(adesso) {
			if ab.VistoTreni[t.Chiave()].Finito {
				continue
			}
			if k := t.Chiave(); !visti[k] {
				visti[k] = true
				// Senza la stazione di salita: qui il treno è un viaggio da
				// leggere, e a chi lo aspetta e dove ci pensa la
				// riconciliazione, abbonato per abbonato.
				out = append(out, TrenoSeguito{Origine: t.Origine, Numero: t.Numero, Data: t.Data})
			}
		}
	}
	// Ordine stabile: il tetto per giro taglia in fondo, e senza un ordine
	// taglierebbe treni diversi a ogni giro.
	slices.SortFunc(out, func(x, y TrenoSeguito) int { return strings.Compare(x.Chiave(), y.Chiave()) })
	return out
}

// PreavvisiDovuti sono le stazioni di cui serve il tabellone in questo giro:
// quelle da cui qualcuno sta per salire su un abituale senza averne ancora
// avuto il preavviso. Ordinate e senza doppioni, come TreniSeguiti.
func (a *Abbonati) PreavvisiDovuti(adesso time.Time) []int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var out []int
	for _, ab := range a.m {
		for _, t := range ab.inAttesa(adesso) {
			if t.preavvisoDovuto(ab.VistoTreni[t.Chiave()], adesso) && !slices.Contains(out, t.Da) {
				out = append(out, t.Da)
			}
		}
	}
	slices.Sort(out)
	return out
}

// Dimentica toglie un abbonamento. Lo si chiama quando il servizio push
// risponde che quell'indirizzo non esiste più: il telefono ha disinstallato
// l'app o revocato il permesso, e insistere è solo traffico.
func (a *Abbonati) Dimentica(endpoint string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, c := a.m[endpoint]; !c {
		return
	}
	delete(a.m, endpoint)
	if err := a.scrivi(); err != nil {
		log.Printf("abbonamenti: scrittura su %s: %v", a.percorso, err)
	}
}

// Riepilogo descrive ogni abbonamento in una riga, per il log d'avvio.
func (a *Abbonati) Riepilogo() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]string, 0, len(a.m))
	for _, ab := range a.m {
		out = append(out, fmt.Sprintf("%s: %d linee (%s), %d treni, %d abituali — %s",
			breve(ab.Sottoscrizione.Endpoint), len(ab.Linee),
			strings.Join(ab.Linee, " "), len(ab.Treni), len(ab.Abituali), ab.fasceScritte()))
	}
	slices.Sort(out)
	return out
}

// PerLinea restituisce chi segue quella linea.
func (a *Abbonati) PerLinea(codice string) []Abbonamento {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var out []Abbonamento
	for _, ab := range a.m {
		if slices.Contains(ab.Linee, codice) {
			out = append(out, ab)
		}
	}
	return out
}

// scrivi va chiamata con il lock preso.
func (a *Abbonati) scrivi() error {
	if a.percorso == "" {
		return nil
	}
	b, err := json.Marshal(a.elenco())
	if err != nil {
		return err
	}
	return scriviAtomico(a.percorso, b, 0o600)
}

// scriviAtomico scrive accanto e rinomina. Il rename è atomico sullo stesso
// filesystem, quindi il file buono o è quello vecchio o è quello nuovo, mai
// mezzo dell'uno e mezzo dell'altro: un riavvio a metà scrittura perderebbe
// altrimenti tutti gli abbonati insieme.
func scriviAtomico(percorso string, dati []byte, modo os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(percorso), "."+filepath.Base(percorso)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(modo); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(dati); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), percorso)
}

// Le due forme che si accettano come coordinate di un treno. Sono le stesse
// che il tabellone applica sulle proprie rotte (`internal/api`): lì arrivano
// dalla query, qui dal corpo di un POST, ma finiscono nello stesso URL verso
// ViaggiaTreno e meritano lo stesso sospetto.
var (
	codiceVT    = regexp.MustCompile(`^[A-Z]{1,2}[0-9]{4,6}$`)
	numeroTreno = regexp.MustCompile(`^[0-9A-Za-z]{1,10}$`)
)

func valida(ab Abbonamento) error {
	u, err := url.Parse(ab.Sottoscrizione.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("endpoint non valido")
	}
	if len(ab.Linee) > MaxLineePerAbbonamento {
		return errors.New("troppe linee")
	}
	if len(ab.Treni) > MaxTreniPerAbbonamento {
		return errors.New("troppi treni")
	}
	for _, t := range ab.Treni {
		// Le tre coordinate finiscono dentro un URL verso ViaggiaTreno, come
		// già fa il tabellone con le sue: si accetta solo la forma dei codici
		// veri, non quello che arriva.
		if !codiceVT.MatchString(t.Origine) || !numeroTreno.MatchString(t.Numero) || t.Data <= 0 {
			return errors.New("treno non valido")
		}
	}
	if len(ab.Abituali) > MaxAbitualiPerAbbonamento {
		return errors.New("troppi treni abituali")
	}
	for _, t := range ab.Abituali {
		// Le stesse coordinate dei segnalibri, e lo stesso sospetto: finiscono
		// nell'URL verso ViaggiaTreno, e la stazione in quello verso RFI.
		if err := t.valida(); err != nil {
			return err
		}
	}
	if len(ab.Fasce) > MaxFasce {
		return errors.New("troppe fasce")
	}
	for _, f := range ab.Fasce {
		if err := f.valida(); err != nil {
			return fmt.Errorf("fascia %s-%s: %w", f.Da, f.A, err)
		}
	}
	// Un fuso che questo binario non conosce si rifiuta subito. Accettarlo
	// vorrebbe dire tenere fasce che si leggono su un quadrante diverso da
	// quello su cui sono state scritte, e accorgersene alle cinque del mattino.
	if ab.Zona != "" {
		if _, err := time.LoadLocation(ab.Zona); err != nil {
			return errors.New("fuso orario non riconosciuto")
		}
	}
	// Senza chiavi non si puo' cifrare niente: l'abbonamento sarebbe accettato
	// e poi fallirebbe a ogni invio, in silenzio.
	if !ab.vuoto() &&
		(ab.Sottoscrizione.Keys.Auth == "" || ab.Sottoscrizione.Keys.P256dh == "") {
		return errors.New("chiavi mancanti")
	}
	return nil
}
