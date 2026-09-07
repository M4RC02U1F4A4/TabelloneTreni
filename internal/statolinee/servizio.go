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

// SorgenteAvvisi è la parte facoltativa: una sorgente che sa anche dire perché
// una linea non è regolare. Sta a parte perché il servizio funziona lo stesso
// senza, e i test del ciclo di lettura non hanno motivo di implementarla.
type SorgenteAvvisi interface {
	Avvisi(ctx context.Context, codice string) ([]trenord.Avviso, error)
}

// MaxAvvisiPerGiro è l'ultima difesa sul numero di linee interrogate a ogni
// lettura. Non dovrebbe mai scattare — le linee seguite sono due o tre — ma il
// dettaglio di una linea pesa oltre 130 KB, e un tetto sul traffico verso un
// servizio altrui è il genere di cosa che si mette prima di averne bisogno.
const MaxAvvisiPerGiro = 12

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
	s.leggiAvvisi(ctx, linee)
}

// daInterrogare sceglie di quali linee chiedere le comunicazioni: solo quelle
// che qualcuno segue davvero.
//
// Il dettaglio di una linea pesa oltre 130 KB, quasi tutto elenco di stazioni.
// Prenderle tutte, o anche solo tutte quelle non regolari — un giorno storto ne
// ha una dozzina — vorrebbe dire chiedere a Trenord megabyte ogni cinque minuti
// per un testo che in quel momento non sta leggendo nessuno. Con questa regola
// il costo è proporzionale a quanto la cosa serve: nessun abbonato, nessuna
// richiesta.
//
// Vale anche per gli scioperi, che Trenord pubblica come comunicazioni sulle
// linee interessate: arrivano su quelle che segui, che sono le uniche per cui
// uno sciopero cambia la giornata.
func (s *Servizio) daInterrogare(linee []trenord.Linea) []trenord.Linea {
	if s.abbonati == nil {
		return nil
	}
	var scelte []trenord.Linea
	for _, l := range linee {
		if len(scelte) >= MaxAvvisiPerGiro {
			break
		}
		if len(s.abbonati.PerLinea(l.Codice)) > 0 {
			scelte = append(scelte, l)
		}
	}
	return scelte
}

func (s *Servizio) leggiAvvisi(ctx context.Context, linee []trenord.Linea) {
	fonte, ok := s.sorgente.(SorgenteAvvisi)
	if !ok {
		return
	}
	for _, l := range s.daInterrogare(linee) {
		c, annulla := context.WithTimeout(ctx, 30*time.Second)
		avvisi, err := fonte.Avvisi(c, l.Codice)
		annulla()
		if err != nil {
			log.Printf("avvisi di %s: %v", l.Codice, err)
			continue
		}
		for _, a := range s.registro.MettiAvvisi(l.Codice, avvisi) {
			log.Printf("%s avviso nuovo: %.80s", l.Codice, a.Testo)
			if s.notificatore != nil {
				go s.notificatore.AvvisaComunicazione(context.WithoutCancel(ctx), l, a)
			}
		}
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

// lineaJSON è una linea con attaccate le sue comunicazioni, quando le
// abbiamo: il client le vuole insieme, e chiederle a parte vorrebbe dire una
// richiesta per linea per una cosa che quasi sempre non c'è.
type lineaJSON struct {
	trenord.Linea
	Avvisi []trenord.Avviso `json:"notices,omitempty"`
}

func (s *Servizio) linee(w http.ResponseWriter, r *http.Request) {
	linee, quando := s.registro.Linee()
	if quando.IsZero() {
		http.Error(w, `{"error":"stato non ancora disponibile"}`, http.StatusServiceUnavailable)
		return
	}
	fuori := make([]lineaJSON, 0, len(linee))
	for _, l := range linee {
		fuori = append(fuori, lineaJSON{Linea: l, Avvisi: s.registro.AvvisiDi(l.Codice)})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]any{
		"updated": quando.UTC().Format(time.RFC3339),
		"lines":   fuori,
	})
}
