package statolinee

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
)

// Abbonamento è un telefono che vuole essere avvisato su certe linee.
type Abbonamento struct {
	Sottoscrizione webpush.Subscription `json:"subscription"`
	Linee          []string             `json:"lines"`

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

var roma = func() *time.Location {
	l, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		return time.UTC
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

// Registra aggiunge o aggiorna un abbonamento. Un elenco di linee vuoto lo
// cancella: è lo stesso gesto visto dall'altra parte — chi spegne l'ultima
// campanella non vuole più essere avvisato, e tenerne memoria non serve a
// nessuno.
func (a *Abbonati) Registra(ab Abbonamento) error {
	if err := valida(ab); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(ab.Linee) == 0 {
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
		ab.Visto = potaVisto(precedente.Visto, ab.Linee)
		a.m[ab.Sottoscrizione.Endpoint] = ab
	}
	return a.scrivi()
}

// potaVisto tiene la memoria delle sole linee ancora seguite. Chi spegne una
// campanella e la riaccende un mese dopo ricomincia da capo, che è giusto: di
// quel mese non gli è stato raccontato niente, e riprendere il filo da dove era
// vorrebbe dire annunciargli un cambio vecchio di settimane.
func potaVisto(visto map[string]Visto, linee []string) map[string]Visto {
	if len(visto) == 0 {
		return nil
	}
	out := make(map[string]Visto, len(linee))
	for _, c := range linee {
		if v, cera := visto[c]; cera {
			out[c] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Tutti restituisce gli abbonamenti, in copia: chi li scorre per notificare fa
// richieste di rete lente, e tenere il lock per tutto quel tempo bloccherebbe
// anche chi si sta solo abbonando.
func (a *Abbonati) Tutti() []Abbonamento {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]Abbonamento, 0, len(a.m))
	for _, ab := range a.m {
		out = append(out, ab)
	}
	return out
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
	a.scrivi()
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
	a.scrivi()
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
	elenco := make([]Abbonamento, 0, len(a.m))
	for _, ab := range a.m {
		elenco = append(elenco, ab)
	}
	b, err := json.Marshal(elenco)
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

func valida(ab Abbonamento) error {
	u, err := url.Parse(ab.Sottoscrizione.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("endpoint non valido")
	}
	if len(ab.Linee) > MaxLineePerAbbonamento {
		return errors.New("troppe linee")
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
	if len(ab.Linee) > 0 && (ab.Sottoscrizione.Keys.Auth == "" || ab.Sottoscrizione.Keys.P256dh == "") {
		return errors.New("chiavi mancanti")
	}
	return nil
}
