// Package limite è un secchio di gettoni per chiave: quante richieste si
// lasciano passare per indirizzo, o in tutto verso un servizio altrui.
package limite

import (
	"sync"
	"time"
)

// maxChiavi è quanti secchi si tengono in memoria. Oltre, si buttano quelli
// già pieni, che sono identici a uno nuovo.
const maxChiavi = 10_000

type Secchi struct {
	mu     sync.Mutex
	secchi map[string]*secchio
	ritmo  float64 // gettoni al secondo
	scorta float64 // gettoni di un secchio pieno
	ora    func() time.Time
}

type secchio struct {
	gettoni float64
	ultimo  time.Time
}

// Nuovo concede scorta richieste di fila, e poi una ogni ogni.
func Nuovo(scorta int, ogni time.Duration) *Secchi {
	return &Secchi{
		secchi: map[string]*secchio{},
		ritmo:  1 / ogni.Seconds(),
		scorta: float64(scorta),
		ora:    time.Now,
	}
}

// Consenti dice se la chiave ha ancora un gettone, e se sì lo spende.
func (s *Secchi) Consenti(chiave string) bool {
	adesso := s.ora()
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.secchi[chiave]
	if b == nil {
		if len(s.secchi) >= maxChiavi {
			s.sfoltisci(adesso)
		}
		b = &secchio{gettoni: s.scorta, ultimo: adesso}
		s.secchi[chiave] = b
	}
	b.gettoni = min(s.scorta, b.gettoni+adesso.Sub(b.ultimo).Seconds()*s.ritmo)
	b.ultimo = adesso
	if b.gettoni < 1 {
		return false
	}
	b.gettoni--
	return true
}

// sfoltisci butta i secchi che nel frattempo si sono riempiti: dimenticarli
// non cambia niente. Se non basta si riparte da zero.
//
// ponytail: azzerare regala una scorta piena a chi stava al limite; capita
// solo con diecimila indirizzi attivi insieme, e lì serve un limite a monte.
func (s *Secchi) sfoltisci(adesso time.Time) {
	pieno := time.Duration(s.scorta / s.ritmo * float64(time.Second))
	for k, b := range s.secchi {
		if adesso.Sub(b.ultimo) >= pieno {
			delete(s.secchi, k)
		}
	}
	if len(s.secchi) >= maxChiavi {
		clear(s.secchi)
	}
}
