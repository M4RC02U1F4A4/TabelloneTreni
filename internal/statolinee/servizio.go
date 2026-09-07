package statolinee

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"regexp"
	"sync"
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
	Dettaglio(ctx context.Context, codice string) (*trenord.Dettaglio, error)
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

	// Le comunicazioni si chiedono anche a richiesta, quando qualcuno apre una
	// riga. Una voce per linea con il suo lucchetto, come fa il tabellone con
	// RFI: dieci persone che aprono la stessa linea nello stesso momento
	// producono una sola richiesta a Trenord.
	muRichieste sync.Mutex
	richieste   map[string]*richiestaAvvisi
}

type richiestaAvvisi struct {
	mu      sync.Mutex
	scadeIl time.Time
}

func Nuovo(s Sorgente) *Servizio {
	return &Servizio{sorgente: s, registro: NuovoRegistro(), richieste: map[string]*richiestaAvvisi{}}
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
	// L'elenco serve ai pallini, non alle notifiche. Lo stesso indirizzo
	// risponde da backend che non concordano, e senza un orario non c'è modo di
	// sapere quale delle due risposte sia quella di adesso: un pallino sbagliato
	// per cinque minuti non fa danno, una notifica sbagliata sì. Le notifiche
	// nascono dal dettaglio della linea, che l'orario ce l'ha.
	for _, c := range s.registro.Aggiorna(linee, time.Now()) {
		log.Printf("elenco: %s %s: %s -> %s", c.Linea.Codice, c.Linea.Nome, c.Prima, c.Linea.Stato)
	}
	s.leggiDettagli(ctx, linee)
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

func (s *Servizio) leggiDettagli(ctx context.Context, linee []trenord.Linea) {
	for _, l := range s.daInterrogare(linee) {
		if _, err := s.ChiediDettaglio(ctx, l.Codice); err != nil {
			log.Printf("dettaglio di %s: %v", l.Codice, err)
		}
	}
}

// ChiediDettaglio restituisce le comunicazioni di una linea, andandole a
// prendere se quelle che abbiamo sono scadute, e per strada aggiorna quello che
// si sa del suo stato.
//
// Le comunicazioni le scrive una persona e cambiano di rado: tenerle per un
// giro di lettura è abbastanza per non chiedere due volte la stessa cosa, e
// abbastanza poco perché chi apre una riga veda quello che c'è adesso.
func (s *Servizio) ChiediDettaglio(ctx context.Context, codice string) ([]trenord.Avviso, error) {
	fonte, ok := s.sorgente.(SorgenteAvvisi)
	if !ok {
		return nil, nil
	}

	s.muRichieste.Lock()
	r := s.richieste[codice]
	if r == nil {
		r = &richiestaAvvisi{}
		s.richieste[codice] = r
	}
	s.muRichieste.Unlock()

	// Il lucchetto è sulla singola linea: chi chiede la stessa aspetta, chi ne
	// chiede un'altra va per conto suo.
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Now().Before(r.scadeIl) {
		return s.registro.AvvisiDi(codice), nil
	}

	// La lettura non è appesa a chi l'ha chiesta: se il telefono rinuncia — o
	// se rinuncia il tabellone, che aspetta meno di noi — il lavoro quasi
	// finito finisce comunque in cache, e il tocco successivo è immediato
	// invece di ricominciare da capo.
	c, annulla := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer annulla()
	d, err := fonte.Dettaglio(c, codice)
	if err != nil {
		// Fallita la lettura si serve quello che c'è, se c'è: un avviso di
		// mezz'ora fa è meglio di un errore in faccia a chi ha aperto la riga.
		if vecchi := s.registro.AvvisiDi(codice); vecchi != nil {
			return vecchi, nil
		}
		return nil, err
	}
	r.scadeIl = time.Now().Add(Intervallo)

	novita := s.registro.MettiDettaglio(codice, s.nomeDi(codice), d)
	if s.notificatore != nil {
		// Le notifiche partono fuori dal giro: sono una richiesta di rete per
		// destinatario verso un servizio altrui.
		go s.notificatore.Annuncia(context.WithoutCancel(ctx), novita)
	}
	if novita.Cambio != nil {
		c := novita.Cambio
		log.Printf("%s %s: %s -> %s", codice, c.Linea.Nome, c.Prima, c.Linea.Stato)
	}
	for _, a := range novita.Avvisi {
		log.Printf("%s avviso nuovo: %.80s", codice, a.Testo)
	}
	return d.Avvisi, nil
}

// nomeDi ritrova il nome per esteso di una linea, che è quello che finisce nel
// titolo della notifica: il codice da solo non dice niente a nessuno.
func (s *Servizio) nomeDi(codice string) string {
	linee, _ := s.registro.Linee()
	for _, l := range linee {
		if l.Codice == codice {
			return l.Nome
		}
	}
	return codice
}

func (s *Servizio) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /linee", s.linee)
	mux.HandleFunc("GET /avvisi", s.avvisiLinea)
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

// codiceLinea limita quello che si accetta come nome di linea: la stringa
// finisce in una richiesta verso Trenord, e i codici veri sono lettere, cifre e
// underscore ("S2", "RE_13", "R16").
var codiceLinea = regexp.MustCompile(`^[A-Za-z0-9_]{1,10}$`)

func (s *Servizio) avvisiLinea(w http.ResponseWriter, r *http.Request) {
	codice := r.URL.Query().Get("linea")
	if !codiceLinea.MatchString(codice) {
		http.Error(w, `{"error":"linea non valida"}`, http.StatusBadRequest)
		return
	}
	avvisi, err := s.ChiediDettaglio(r.Context(), codice)
	if err != nil {
		log.Printf("avvisi di %s: %v", codice, err)
		http.Error(w, `{"error":"avvisi non disponibili"}`, http.StatusBadGateway)
		return
	}
	if avvisi == nil {
		avvisi = []trenord.Avviso{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]any{"notices": avvisi})
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
