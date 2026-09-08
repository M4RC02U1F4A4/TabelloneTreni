// Package board mette insieme il tabellone di RFI, la cache e il filtro per
// destinazione: è il servizio che l'API espone.
package board

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/rfi"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/stations"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/vt"
)

// TTL è quanto a lungo un tabellone resta valido in cache.
//
// Sta sotto al minuto con cui i client si aggiornano, così un refresh trova
// quasi sempre dati freschi; ma è abbastanza lungo da far sì che dieci persone
// sulla stessa stazione producano comunque una sola richiesta a RFI ogni mezzo
// minuto, invece di dieci al minuto.
const TTL = 30 * time.Second

type Source interface {
	Fetch(ctx context.Context, placeID int, arrivals bool) (*rfi.Board, error)
}

// Live è la seconda fonte: ViaggiaTreno, che misura il ritardo sul treno invece
// di stamparlo sul tabellone, e che dei binari dichiara sia il previsto sia
// quello assegnato davvero. È un'interfaccia perché è facoltativa — un Service
// senza resta un servizio che funziona, solo con qualche colonna in meno — e
// perché i test non devono uscire in rete per averla.
type Live interface {
	Treni(ctx context.Context, codice string, arrivi bool) (map[string]vt.Treno, error)
	Andamento(ctx context.Context, codOrigine, numero string, data int64) (*vt.Andamento, error)
}

type Service struct {
	src      Source
	live     Live
	catalogo *stations.Catalogo

	mu    sync.Mutex
	cache map[chiave]*voce

	// Cache degli andamenti, separata da quella dei tabelloni: si riempie solo
	// con i treni che qualcuno apre davvero, che sono pochi, ma va a mani sul
	// dito di chi tocca la stessa scheda due volte di seguito.
	muAnd  sync.Mutex
	viaggi map[string]*viaggio

	// Cache delle sole fermate, per i treni di cui RFI non le pubblica.
	// Separata da viaggi perché ha una scadenza diversa: dove si trova un treno
	// cambia di minuto in minuto — ed è per questo che viaggi vive trenta
	// secondi — mentre le fermate di un treno sono le stesse per tutto il
	// giorno. Il giorno di partenza sta nella chiave, quindi la voce di ieri
	// non viene più cercata da nessuno e un TTL non serve.
	muFer   sync.Mutex
	fermate map[string][]vt.Fermata
}

type viaggio struct {
	andamento *vt.Andamento
	scadeIl   time.Time
}

type chiave struct {
	placeID  int
	arrivals bool
}

type voce struct {
	mu      sync.Mutex // serializza i fetch sulla stessa chiave
	board   *rfi.Board
	scadeIl time.Time
	// Quello che ViaggiaTreno ha detto sugli stessi treni. Sul tabellone ne
	// finiscono ritardo e binario, ma qui restano anche le coordinate con cui
	// chiedere l'andamento del singolo treno quando qualcuno apre la scheda.
	live map[string]vt.Treno
}

func New(src Source, cat *stations.Catalogo) *Service {
	return &Service{
		src:      src,
		catalogo: cat,
		cache:    map[chiave]*voce{},
		viaggi:   map[string]*viaggio{},
		fermate:  map[string][]vt.Fermata{},
	}
}

// ConLive attacca la seconda fonte. Senza, il servizio si comporta come prima e
// i treni escono con il solo tabellone di RFI.
func (s *Service) ConLive(l Live) *Service {
	s.live = l
	return s
}

// Result è quello che l'API restituisce: il tabellone, più il contesto della
// tratta quando la richiesta è filtrata.
type Result struct {
	*rfi.Board
	// From e To sono i nomi ufficiali delle stazioni scelte, che non
	// coincidono con quelli stampati sul tabellone.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// Filtered dice se Trains è già ristretto ai treni utili alla tratta.
	Filtered bool `json:"filtered"`
	// StopsUnavailable segnala che su questo tabellone il filtro non è
	// applicabile perché RFI non pubblica le fermate — succede sugli arrivi.
	StopsUnavailable bool `json:"stopsUnavailable,omitempty"`
	// Total è quanti treni c'erano prima del filtro.
	Total int `json:"total"`
}

// Get restituisce il tabellone di from. Se to è diverso da zero, tiene solo i
// treni che fermano lì e annota per ciascuno l'orario di arrivo.
func (s *Service) Get(ctx context.Context, from int, arrivals bool, to int) (*Result, error) {
	if s.catalogo.ByID(from) == nil {
		return nil, fmt.Errorf("stazione %d sconosciuta", from)
	}
	b, err := s.tabellone(ctx, from, arrivals)
	if err != nil {
		return nil, err
	}

	res := &Result{Board: b, Total: len(b.Trains)}
	if st := s.catalogo.ByID(from); st != nil {
		res.From = st.Name
	}
	if to == 0 {
		return res, nil
	}

	dest := s.catalogo.ByID(to)
	if dest == nil {
		return nil, fmt.Errorf("stazione %d sconosciuta", to)
	}
	res.To = dest.Name

	// Sugli arrivi le fermate non ci sono proprio: filtrare vorrebbe dire
	// nascondere tutto. Meglio restituire il tabellone intero e dirlo.
	if arrivals {
		res.StopsUnavailable = true
		return res, nil
	}

	m := s.catalogo.Matcher(to)
	// RFI non pubblica le fermate di tutti i treni: quelle dei cancellati non
	// ci sono affatto, e su qualche altro treno la cella dei dettagli è vuota.
	// Cercarle solo lì dentro li farebbe sparire dalla tratta, e chi aspetta un
	// treno cancellato non potrebbe distinguere "è cancellato" da "non è in
	// questa fascia oraria" — è il caso, in un giorno di sciopero, che ha fatto
	// nascere questa seconda lettura.
	supplenti := s.fermateSupplenti(ctx, from, arrivals, b, dest)
	codiciDest := dest.CodiciVT()

	// La lista filtrata deve restare nell'ordine del tabellone, che è quello
	// degli orari: le fermate mancanti si risolvono tutte prima, qui si scorre
	// b.Trains una volta sola.
	filtrati := make([]rfi.Train, 0, len(b.Trains))
	for riga, t := range b.Trains {
		if fermata := trovaFermata(m, t); fermata != nil {
			t.Arrival = fermata.Time
			filtrati = append(filtrati, t)
			continue
		}
		if len(t.Stops) > 0 {
			continue
		}
		// Le fermate risolte si prendono per riga, non per numero: lo stesso
		// numero può comparire su due righe del tabellone — un treno che si
		// sdoppia, con due destinazioni e due stati — e riscrivere tutte le
		// righe con quel numero metterebbe su una il percorso dell'altra.
		fermate := supplenti[riga]
		i := indiceFermata(fermate, codiciDest)
		if i < 0 {
			continue
		}
		t.Arrival = orario(fermate[i].Programmata)
		// Sul treno cancellato le fermate restano vuote: il frontend apre la
		// scheda solo se ce ne sono, e di un treno cancellato non c'è nessun
		// viaggio da seguire. Sugli altri si riempiono, così la riga si apre
		// come tutte quelle di cui le fermate le ha pubblicate RFI.
		if !t.Cancelled {
			t.Stops = fermateRFI(fermate)
		}
		filtrati = append(filtrati, t)
	}
	// Copia superficiale: il Board sotto sta in cache ed è condiviso, non si
	// può sostituirgli la lista dei treni sotto i piedi.
	filtrato := *b
	filtrato.Trains = filtrati
	res.Board = &filtrato
	res.Filtered = true
	return res, nil
}

// trovaFermata cerca la stazione di destinazione fra le fermate successive.
// La destinazione del treno è già l'ultima voce dell'elenco, quindi non serve
// controllarla a parte.
func trovaFermata(m *stations.Matcher, t rfi.Train) *rfi.Stop {
	for i := range t.Stops {
		if m.Matches(t.Stops[i].Name) {
			return &t.Stops[i]
		}
	}
	return nil
}

// FermateMax è quante liste di fermate restano in cache. Sono i treni di cui
// RFI non pubblica le fermate, visti da tutte le stazioni chieste oggi: pochi,
// ma senza un tetto il processo le accumula finché resta acceso.
//
// ponytail: tetto a 500 voci e scarto di una voce qualsiasi, come per gli
// andamenti. Se un giorno le stazioni chieste fossero tante, si alza il numero;
// un ordine di scarto vero (LRU) solo se si vedesse rifare le stesse richieste.
const FermateMax = 500

// AttesaFermate è quanto si aspetta ViaggiaTreno prima di rispondere comunque.
//
// A cache fredda, in un giorno di sciopero, sono una ventina di richieste a un
// servizio che ha otto secondi di timeout: aspettarle tutte vorrebbe dire una
// pagina che si apre in otto secondi. I treni non ancora risolti semplicemente
// non compaiono in questa passata e arrivano dalla cache al rinfresco dopo,
// mezzo minuto più tardi — una tratta quasi completa subito è più utile di una
// completa fra otto secondi, che nessuno resta a guardare.
const AttesaFermate = 2500 * time.Millisecond

// fermateSupplenti chiede a ViaggiaTreno le fermate dei treni per cui RFI non
// le pubblica e le restituisce per indice di riga del tabellone, già tagliate a
// quelle che il treno ha ancora davanti: è quello che RFI stampa nel popup
// della riga.
//
// Restituisce nil in tutti i casi in cui questa strada non è percorribile —
// nessuna seconda fonte, destinazione senza codice ViaggiaTreno, tabellone
// senza treni da risolvere — e allora il filtro si comporta come prima. Nessun
// errore risale: è una lettura in più, non una da cui dipendere.
func (s *Service) fermateSupplenti(ctx context.Context, placeID int, arrivals bool, b *rfi.Board, dest *stations.Station) map[int][]vt.Fermata {
	if s.live == nil || dest.VT == "" {
		return nil
	}
	live := s.misureDi(placeID, arrivals)

	type richiesta struct {
		riga   int
		numero string
		chiave string
		treno  vt.Treno
	}
	var da []richiesta
	for riga, t := range b.Trains {
		if len(t.Stops) > 0 {
			continue
		}
		numero := strings.TrimSpace(t.Number)
		// La mappa di ViaggiaTreno porta un treno per numero, e di un treno
		// sdoppiato ne conosce uno solo: le due righe leggono quindi le stesse
		// coordinate, e su una delle due il filtro può sbagliare percorso. Non
		// è risolvibile con i dati che ci sono — RFI e ViaggiaTreno non hanno
		// altro identificatore in comune — ed è la stessa scelta che unisci fa
		// già per il ritardo misurato.
		m, ok := live[numero]
		if !ok || m.CodOrigine == "" || m.DataPartenza == 0 {
			continue
		}
		da = append(da, richiesta{riga, numero, chiaveViaggio(m.CodOrigine, numero, m.DataPartenza), m})
	}
	if len(da) == 0 {
		return nil
	}

	// Le richieste che mancano partono tutte insieme: in fila costerebbero la
	// somma di una ventina di servizi lenti. Il contesto non è quello di chi ha
	// chiesto la pagina — se se ne va, le goroutine finiscono comunque di
	// riempire la cache, che è quello che rende utile il rinfresco dopo.
	ctxFer, annulla := context.WithTimeout(context.WithoutCancel(ctx), 25*time.Second)
	var wg sync.WaitGroup
	// Due righe con lo stesso numero chiedono le stesse coordinate: la
	// richiesta si fa una volta, il risultato lo leggono entrambe.
	chiesti := map[string]bool{}
	for _, r := range da {
		s.muFer.Lock()
		_, gia := s.fermate[r.chiave]
		s.muFer.Unlock()
		if gia || chiesti[r.chiave] {
			continue
		}
		chiesti[r.chiave] = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, err := s.live.Andamento(ctxFer, r.treno.CodOrigine, r.numero, r.treno.DataPartenza)
			if err != nil {
				// L'errore non si memorizza: è il servizio che non risponde
				// adesso, non un treno che non esiste, e fra mezzo minuto la
				// stessa domanda può avere una risposta.
				log.Printf("fermate da ViaggiaTreno per il treno %s: %v", r.numero, err)
				return
			}
			var fermate []vt.Fermata
			if a != nil {
				fermate = a.Fermate
			}
			s.muFer.Lock()
			defer s.muFer.Unlock()
			// Anche l'esito negativo va in cache: in un giorno di sciopero
			// ViaggiaTreno non conosce venti treni su quaranta, e senza
			// ricordarselo si ripeterebbero venti richieste a vuoto ogni mezzo
			// minuto, per sempre.
			sfoltisci(s.fermate, FermateMax)
			s.fermate[r.chiave] = fermate
		}()
	}

	fatto := make(chan struct{})
	go func() { wg.Wait(); annulla(); close(fatto) }()
	select {
	case <-fatto:
	case <-time.After(AttesaFermate):
	}

	codiciPartenza := s.catalogo.ByID(placeID).CodiciVT()
	out := make(map[int][]vt.Fermata, len(da))
	s.muFer.Lock()
	defer s.muFer.Unlock()
	for _, r := range da {
		fermate := s.fermate[r.chiave]
		if len(fermate) == 0 {
			continue
		}
		// L'elenco di ViaggiaTreno parte dall'origine del treno, che spesso è
		// prima della stazione da cui lo si guarda: senza il taglio la scheda
		// mostrerebbe fermate già passate, e la destinazione risulterebbe
		// servita anche da un treno che da lì è già transitato. Se la stazione
		// non c'è nell'elenco si tiene tutto: meglio una fermata di troppo che
		// un treno che sparisce dalla tratta.
		if i := indiceFermata(fermate, codiciPartenza); i >= 0 {
			fermate = fermate[i+1:]
		}
		out[r.riga] = fermate
	}
	return out
}

// indiceFermata trova una stazione fra le fermate di ViaggiaTreno.
//
// L'aggancio è sul codice stazione, che è un confronto esatto: il Matcher deve
// invece ricondurre a una stazione le abbreviazioni che RFI stampa, e su un
// treno che non compare da nessun'altra parte conviene la strada che non deve
// indovinare. I codici sono più di uno quando la stazione ha due livelli.
func indiceFermata(fermate []vt.Fermata, codici []string) int {
	for i := range fermate {
		if slices.Contains(codici, fermate[i].Codice) {
			return i
		}
	}
	return -1
}

// fermateRFI riscrive le fermate di ViaggiaTreno nella forma del tabellone.
// L'orario è quello previsto, come nel popup di RFI: quello reale ce l'hanno
// solo le fermate già servite, e a una lista di fermate future non serve.
func fermateRFI(fermate []vt.Fermata) []rfi.Stop {
	out := make([]rfi.Stop, 0, len(fermate))
	for _, f := range fermate {
		out = append(out, rfi.Stop{Name: f.Nome, Time: orario(f.Programmata)})
	}
	return out
}

// roma è il fuso in cui vanno letti gli orari dei treni italiani: quelli che
// arrivano da ViaggiaTreno sono istanti, e chi guarda il tabellone può stare
// altrove. Il database dei fusi è dentro il binario (vedi l'import in main.go);
// se anche così mancasse, un orario sbagliato di un'ora sarebbe peggio di
// nessun orario, quindi non se ne mostra nessuno.
var roma, erroreFuso = time.LoadLocation("Europe/Rome")

func orario(t time.Time) string {
	if t.IsZero() || erroreFuso != nil {
		return ""
	}
	return t.In(roma).Format("15:04")
}

// sfoltisci tiene una cache sotto il suo tetto buttando una voce qualsiasi.
// Sono tutte equivalenti — scadute o quasi — e tenere un ordine di scarto
// costerebbe più di quanto valga.
func sfoltisci[T any](m map[string]T, max int) {
	for len(m) >= max {
		for k := range m {
			delete(m, k)
			break
		}
	}
}

func (s *Service) tabellone(ctx context.Context, placeID int, arrivals bool) (*rfi.Board, error) {
	k := chiave{placeID, arrivals}

	s.mu.Lock()
	v := s.cache[k]
	if v == nil {
		v = &voce{}
		s.cache[k] = v
	}
	s.mu.Unlock()

	// Il lock per chiave fa sì che, se dieci richieste arrivano insieme a cache
	// scaduta, una sola vada a RFI e le altre aspettino il suo risultato.
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.board != nil && time.Now().Before(v.scadeIl) {
		return v.board, nil
	}

	// Il fetch è condiviso da tutti quelli in attesa su questa chiave, quindi
	// non può dipendere da chi è arrivato per primo: se quel client chiude la
	// pagina, gli altri si vedrebbero fallire una richiesta ancora valida.
	fetchCtx, annulla := context.WithTimeout(context.WithoutCancel(ctx), 25*time.Second)
	defer annulla()

	// Le due fonti si leggono insieme, non in fila: sono indipendenti, e messe
	// in sequenza la pagina aspetterebbe la somma di due servizi lenti invece
	// del più lento dei due.
	live := s.leggiLive(fetchCtx, placeID, arrivals)

	b, err := s.src.Fetch(fetchCtx, placeID, arrivals)
	if err != nil {
		<-live // la goroutine ha il posto in un canale con buffer: non resta appesa
		// Un tabellone scaduto è più utile di un errore: RFI ogni tanto non
		// risponde, e mostrare dati di mezzo minuto fa è meglio di una pagina
		// vuota. Oltre il minuto di ritardo, però, l'errore va detto.
		if v.board != nil && time.Since(v.scadeIl) < time.Minute {
			return v.board, nil
		}
		return nil, err
	}
	// Le misure si attaccano al tabellone appena arrivato, prima che entri in
	// cache: da qui in poi è un tabellone solo, che porta con sé tutt'e due le
	// letture, e nessuno a valle deve sapere che le fonti erano due.
	misure := <-live
	unisci(b, misure)

	v.board, v.scadeIl, v.live = b, time.Now().Add(TTL), misure
	return b, nil
}

// leggiLive avvia la lettura di ViaggiaTreno e restituisce il canale da cui
// arriverà. Il canale ha un posto in buffer perché chi l'ha avviata possa
// andarsene senza lasciare la goroutine appesa a scrivere.
//
// Consegna nil in tutti i casi in cui la misura non c'è: nessuna seconda fonte
// configurata, stazione senza codice ViaggiaTreno, oppure servizio che non
// risponde. È una lettura in più, non una da cui dipendere: un tabellone col
// solo ritardo di RFI è quello che l'app mostrava fino a ieri.
func (s *Service) leggiLive(ctx context.Context, placeID int, arrivi bool) <-chan map[string]vt.Treno {
	ch := make(chan map[string]vt.Treno, 1)
	st := s.catalogo.ByID(placeID)
	if s.live == nil || st == nil || st.VT == "" {
		ch <- nil
		return ch
	}
	go func() { ch <- s.treniDa(ctx, st, arrivi) }()
	return ch
}

// treniDa interroga tutti i livelli della stazione e ne fonde le risposte.
//
// Quasi sempre è un codice solo. Dove sono due — una stazione con il piano
// sotterraneo — le due richieste partono insieme, perché in fila costerebbero
// la somma di due servizi lenti, e se una fallisce restano i treni dell'altra:
// mezzo tabellone con i ritardi misurati è meglio di nessuno.
func (s *Service) treniDa(ctx context.Context, st *stations.Station, arrivi bool) map[string]vt.Treno {
	codici := st.CodiciVT()
	risposte := make([]map[string]vt.Treno, len(codici))

	var wg sync.WaitGroup
	for i, codice := range codici {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.live.Treni(ctx, codice, arrivi)
			if err != nil {
				log.Printf("ViaggiaTreno per %s (%s): %v", st.Name, codice, err)
				return
			}
			risposte[i] = r
		}()
	}
	wg.Wait()

	// Con un codice solo si restituisce la mappa com'è, senza ricopiarla.
	if len(risposte) == 1 {
		return risposte[0]
	}
	unione := map[string]vt.Treno{}
	for _, r := range risposte {
		for numero, t := range r {
			// Il primo livello che porta un treno se lo tiene: lo stesso numero
			// su due piani della stessa stazione sarebbe lo stesso treno, e non
			// c'è motivo di preferire la seconda risposta alla prima.
			if _, gia := unione[numero]; !gia {
				unione[numero] = t
			}
		}
	}
	return unione
}

// unisci accoppia le due fonti sul numero di treno.
//
// Il numero è l'unica chiave possibile — RFI e ViaggiaTreno non condividono
// nessun altro identificatore — ed è anche una chiave buona: lo stesso numero
// in due tabelloni vicini è lo stesso treno, quindi anche un accoppiamento
// sbagliato fra stazioni finirebbe per non trovare niente invece che per
// mostrare il ritardo di un altro treno.
func unisci(b *rfi.Board, live map[string]vt.Treno) {
	if len(live) == 0 {
		return
	}
	for i := range b.Trains {
		t, ok := live[strings.TrimSpace(b.Trains[i].Number)]
		if !ok {
			continue
		}
		if t.Ritardo != nil {
			minuti := *t.Ritardo
			b.Trains[i].LiveDelay = &minuti
		}
		if t.Cambiato() {
			b.Trains[i].PlatformChanged = true
			b.Trains[i].PlatformScheduled = t.BinarioProgrammato
			b.Trains[i].PlatformActual = t.BinarioEffettivo
		}
	}
}

// AndamentoMax è quanti viaggi restano in cache. Sono le schede aperte di
// recente: poche per definizione, ma senza un tetto il processo le accumula
// finché resta acceso.
const AndamentoMax = 200

// Andamento restituisce il viaggio di un treno del tabellone: dove si trova
// adesso e a che ora è passato dalle fermate che ha già servito.
//
// Il treno si identifica con il tabellone da cui lo si è aperto, perché le
// coordinate che ViaggiaTreno pretende — stazione di origine e giorno di
// partenza, oltre al numero — le ha già lette il tabellone: chiederle al client
// vorrebbe dire fidarsi di quello che rimanda indietro.
//
// Restituisce nil, senza errore, in tutti i casi in cui il dato semplicemente
// non c'è: nessuna seconda fonte, tabellone mai letto, treno che ViaggiaTreno
// non conosce, treno che non traccia.
func (s *Service) Andamento(ctx context.Context, placeID int, arrivals bool, numero string) (*vt.Andamento, error) {
	if s.live == nil {
		return nil, nil
	}
	numero = strings.TrimSpace(numero)

	t, ok := s.misureDi(placeID, arrivals)[numero]
	if !ok || t.CodOrigine == "" || t.DataPartenza == 0 {
		return nil, nil
	}
	return s.Viaggio(ctx, t.CodOrigine, numero, t.DataPartenza)
}

// Viaggio è lo stesso andamento, chiesto con le coordinate invece che con il
// tabellone da cui il treno viene.
//
// Esiste per i treni seguiti, che è il caso in cui un tabellone non c'è: chi
// segue un treno lo guarda dalla home, e più spesso lo guarda mentre ci è
// sopra, quando il treno è già partito e dal tabellone della stazione di
// partenza è sparito da un pezzo. Le coordinate quindi le tiene il telefono, e
// il server le prende per buone dopo averne controllato la forma — vedi
// l'handler, che è dove arrivano da fuori.
func (s *Service) Viaggio(ctx context.Context, codOrigine, numero string, data int64) (*vt.Andamento, error) {
	if s.live == nil {
		return nil, nil
	}
	numero = strings.TrimSpace(numero)

	k := chiaveViaggio(codOrigine, numero, data)
	s.muAnd.Lock()
	if c := s.viaggi[k]; c != nil && time.Now().Before(c.scadeIl) {
		s.muAnd.Unlock()
		return c.andamento, nil
	}
	s.muAnd.Unlock()

	a, err := s.live.Andamento(ctx, codOrigine, numero, data)
	if err != nil {
		return nil, err
	}

	s.muAnd.Lock()
	defer s.muAnd.Unlock()
	sfoltisci(s.viaggi, AndamentoMax)
	s.viaggi[k] = &viaggio{andamento: a, scadeIl: time.Now().Add(TTL)}
	return a, nil
}

// misureDi restituisce quello che ViaggiaTreno ha detto dei treni di un
// tabellone già in cache: da qui vengono le coordinate — stazione di origine e
// giorno di partenza — con cui si chiede il viaggio di un treno singolo. La
// mappa non viene più modificata dopo essere entrata in cache, quindi si
// restituisce com'è invece di ricopiarla.
func (s *Service) misureDi(placeID int, arrivals bool) map[string]vt.Treno {
	s.mu.Lock()
	v := s.cache[chiave{placeID, arrivals}]
	s.mu.Unlock()
	if v == nil {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.live
}

// chiaveViaggio identifica un treno come lo identifica ViaggiaTreno: il numero
// da solo non basta, perché torna ogni giorno e su relazioni diverse.
func chiaveViaggio(codOrigine, numero string, data int64) string {
	return fmt.Sprintf("%s|%s|%d", codOrigine, numero, data)
}
