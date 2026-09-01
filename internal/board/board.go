// Package board mette insieme il tabellone di RFI, la cache e il filtro per
// destinazione: è il servizio che l'API espone.
package board

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/rfi"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/stations"
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

type Service struct {
	src      Source
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
}

func New(src Source, cat *stations.Catalogo) *Service {
	return &Service{src: src, catalogo: cat, cache: map[chiave]*voce{}}
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

	b, err := s.src.Fetch(fetchCtx, placeID, arrivals)
	if err != nil {
		// Un tabellone scaduto è più utile di un errore: RFI ogni tanto non
		// risponde, e mostrare dati di mezzo minuto fa è meglio di una pagina
		// vuota. Oltre il minuto di ritardo, però, l'errore va detto.
		if v.board != nil && time.Since(v.scadeIl) < time.Minute {
			return v.board, nil
		}
		return nil, err
	}
	v.board, v.scadeIl = b, time.Now().Add(TTL)
	return b, nil
}
