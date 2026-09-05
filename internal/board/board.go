// Package board mette insieme il tabellone di RFI, la cache e il filtro per
// destinazione: è il servizio che l'API espone.
package board

import (
	"context"
	"fmt"
	"log"
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
}

type Service struct {
	src      Source
	live     Live
	catalogo *stations.Catalogo

	mu    sync.Mutex
	cache map[chiave]*voce
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
	return &Service{src: src, catalogo: cat, cache: map[chiave]*voce{}}
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
	filtrati := make([]rfi.Train, 0, len(b.Trains))
	for _, t := range b.Trains {
		if fermata := trovaFermata(m, t); fermata != nil {
			t.Arrival = fermata.Time
			filtrati = append(filtrati, t)
		}
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
	go func() {
		r, err := s.live.Treni(ctx, st.VT, arrivi)
		if err != nil {
			log.Printf("ViaggiaTreno per %s (%s): %v", st.Name, st.VT, err)
			r = nil
		}
		ch <- r
	}()
	return ch
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
