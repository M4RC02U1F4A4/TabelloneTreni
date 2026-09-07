package statolinee

import (
	"context"
	"encoding/json"
	"io"
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
	sorgente     Sorgente
	registro     *Registro
	abbonati     *Abbonati
	notificatore *Notificatore
}

func Nuovo(s Sorgente) *Servizio {
	return &Servizio{sorgente: s, registro: NuovoRegistro()}
}

// ConNotifiche accende le notifiche push. Senza, il servizio fa tutto il resto
// come prima: i bollini si vedono, e i cambi restano nel log.
func (s *Servizio) ConNotifiche(ab *Abbonati, n *Notificatore) *Servizio {
	s.abbonati, s.notificatore = ab, n
	return s
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
	cambi := s.registro.Aggiorna(linee, time.Now())
	for _, c := range cambi {
		log.Printf("%s %s: %s -> %s", c.Linea.Codice, c.Linea.Nome, c.Prima, c.Linea.Stato)
	}
	// Le notifiche partono fuori dal giro di lettura: sono una richiesta di
	// rete per destinatario verso un servizio altrui, e farle aspettare qui
	// ritarderebbe la lettura successiva senza motivo.
	if s.notificatore != nil && len(cambi) > 0 {
		go s.notificatore.Avvisa(context.WithoutCancel(ctx), cambi)
	}
}

func (s *Servizio) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /linee", s.linee)
	mux.HandleFunc("GET /push/chiave", s.chiavePush)
	mux.HandleFunc("POST /push/abbonamenti", s.abbonamento)
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

// chiavePush dà al telefono la chiave pubblica VAPID, che gli serve per
// abbonarsi. Vuota significa che le notifiche non sono configurate, ed è un
// caso normale: l'applicazione funziona lo stesso e l'interfaccia lo dice.
func (s *Servizio) chiavePush(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]string{"key": s.notificatore.ChiavePubblica()})
}

// abbonamento registra chi vuole essere avvisato e su quali linee. Un elenco
// di linee vuoto cancella l'abbonamento: è lo stesso gesto visto dall'altra
// parte, quindi una rotta sola invece di due.
func (s *Servizio) abbonamento(w http.ResponseWriter, r *http.Request) {
	if s.abbonati == nil {
		http.Error(w, `{"error":"notifiche non configurate"}`, http.StatusServiceUnavailable)
		return
	}
	var ab Abbonamento
	// Il corpo arriva da fuori: si legge con un tetto, non a fiducia.
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&ab); err != nil {
		http.Error(w, `{"error":"richiesta non leggibile"}`, http.StatusBadRequest)
		return
	}
	if err := s.abbonati.Registra(ab); err != nil {
		log.Printf("abbonamento rifiutato: %v", err)
		http.Error(w, `{"error":"abbonamento non valido"}`, http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
