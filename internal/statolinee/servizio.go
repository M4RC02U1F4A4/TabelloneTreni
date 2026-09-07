package statolinee

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/trenord"
)

// Intervallo fra una lettura e l'altra. Trenord aggiorna i bollini a mano
// dalla sala operativa: più spesso di così si otterrebbero le stesse risposte
// e basta.
const Intervallo = 5 * time.Minute

// Sorgente è da dove arrivano le linee. È un'interfaccia per un motivo solo:
// poter provare il ciclo di lettura senza rete.
type Sorgente interface {
	Fetch(ctx context.Context) ([]trenord.Linea, error)
}

type Servizio struct {
	sorgente Sorgente
	registro *Registro
}

func Nuovo(s Sorgente) *Servizio {
	return &Servizio{sorgente: s, registro: NuovoRegistro()}
}

// Osserva legge subito e poi a ogni intervallo, finché il contesto non finisce.
func (s *Servizio) Osserva(ctx context.Context) {
	s.leggi(ctx)
	t := time.NewTicker(Intervallo)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.leggi(ctx)
		}
	}
}

func (s *Servizio) leggi(ctx context.Context) {
	ctx, annulla := context.WithTimeout(ctx, 30*time.Second)
	defer annulla()

	linee, err := s.sorgente.Fetch(ctx)
	if err != nil {
		// Una lettura fallita non azzera niente: si continua a servire
		// l'ultimo stato buono, che l'orario di aggiornamento dichiara vecchio.
		log.Printf("lettura da Trenord: %v", err)
		return
	}
	// I cambi finiscono nel log: è da qui che passeranno le notifiche, quando
	// ci saranno, ed è già adesso il modo per vedere se il diff funziona.
	for _, c := range s.registro.Aggiorna(linee, time.Now()) {
		log.Printf("%s %s: %s -> %s", c.Linea.Codice, c.Linea.Nome, c.Prima, c.Linea.Stato)
	}
}

func (s *Servizio) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /linee", s.linee)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		// Il servizio è sano quando ha almeno uno stato da servire: appena
		// avviato non ne ha, e non deve ricevere traffico.
		if _, quando := s.registro.Linee(); quando.IsZero() {
			http.Error(w, "nessuna lettura riuscita", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok\n"))
	})
	return mux
}

func (s *Servizio) linee(w http.ResponseWriter, r *http.Request) {
	linee, quando := s.registro.Linee()
	if quando.IsZero() {
		http.Error(w, `{"error":"stato non ancora disponibile"}`, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]any{
		"updated": quando.UTC().Format(time.RFC3339),
		"lines":   linee,
	})
}
