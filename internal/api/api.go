// Package api espone il servizio via HTTP e serve l'interfaccia.
package api

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/board"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/stations"
)

type Server struct {
	svc      *board.Service
	catalogo *stations.Catalogo
	statici  fs.FS

	elencoUnaVolta sync.Once
	elencoBody     []byte
	elencoETag     string
}

func New(svc *board.Service, cat *stations.Catalogo, statici fs.FS) *Server {
	return &Server{svc: svc, catalogo: cat, statici: statici}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stations", s.stazioni)
	mux.HandleFunc("GET /api/board", s.tabellone)
	mux.HandleFunc("GET /api/train", s.treno)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	mux.Handle("GET /", s.fileStatici())
	return comprimi(mux)
}

// stazioni restituisce l'intero catalogo in un colpo solo, come coppie
// [id, nome]. Sono un'ottantina di KB che gzip riduce a una decina: mandarlo
// tutto una volta permette al client di cercare senza toccare più la rete, che
// su mobile è la cosa che si sente di più.
func (s *Server) stazioni(w http.ResponseWriter, r *http.Request) {
	s.elencoUnaVolta.Do(func() {
		coppie := make([][2]any, 0, len(s.catalogo.Elenco))
		for _, st := range s.catalogo.Elenco {
			coppie = append(coppie, [2]any{st.ID, st.Name})
		}
		s.elencoBody, _ = json.Marshal(map[string]any{
			"generated": s.catalogo.Generated,
			"stations":  coppie,
		})
		s.elencoETag = etag(s.elencoBody)
	})
	// Il catalogo cambia quando cambia l'immagine: si può tenere a lungo, e
	// l'ETag copre comunque il caso in cui cambi.
	w.Header().Set("Cache-Control", "public, max-age=86400")
	scriviJSON(w, r, s.elencoBody, s.elencoETag)
}

func (s *Server) tabellone(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, err := strconv.Atoi(q.Get("from"))
	if err != nil || from <= 0 {
		errore(w, http.StatusBadRequest, "parametro 'from' mancante o non valido")
		return
	}
	var to int
	if v := q.Get("to"); v != "" {
		if to, err = strconv.Atoi(v); err != nil || to <= 0 {
			errore(w, http.StatusBadRequest, "parametro 'to' non valido")
			return
		}
	}
	arrivals := q.Get("arrivals") == "true"

	res, err := s.svc.Get(r.Context(), from, arrivals, to)
	if err != nil {
		log.Printf("tabellone from=%d to=%d arrivi=%v: %v", from, to, arrivals, err)
		errore(w, http.StatusBadGateway, "tabellone non disponibile")
		return
	}
	body, err := json.Marshal(res)
	if err != nil {
		errore(w, http.StatusInternalServerError, "errore interno")
		return
	}
	// I dati cambiano in continuazione: il client deve sempre richiedere, ma
	// l'ETag gli risparmia il corpo quando il tabellone non è cambiato — cioè
	// quasi sempre, visto che si aggiorna più spesso di quanto RFI cambi.
	w.Header().Set("Cache-Control", "no-cache")
	scriviJSON(w, r, body, etag(body))
}

// romaOrRomaLess è il fuso in cui vanno letti gli orari dei treni italiani. Il
// database dei fusi è dentro il binario (vedi l'import in main.go), quindi non
// dipende da cosa c'è nell'immagine; se anche così mancasse, gli orari
// verrebbero mostrati in UTC, e allora è meglio non mostrarli affatto che
// mostrarli sbagliati di un'ora.
var roma, erroreFuso = time.LoadLocation("Europe/Rome")

func orario(t time.Time) string {
	if t.IsZero() || erroreFuso != nil {
		return ""
	}
	return t.In(roma).Format("15:04")
}

// fermataJSON è una tappa del viaggio come la vede il client.
type fermataJSON struct {
	Code string `json:"code"`
	Name string `json:"name"`
	// Scheduled e Actual sono orari già formattati: il fuso è una cosa dei
	// treni italiani, non del telefono di chi guarda, che potrebbe essere
	// altrove e vedrebbe orari spostati di un'ora.
	Scheduled string `json:"scheduled"`
	Actual    string `json:"actual,omitempty"`
	Delay     int    `json:"delay,omitempty"`
	Passed    bool   `json:"passed,omitempty"`
	// Chosen marca la fermata dove si scende, quando la richiesta la nomina.
	// Lo decide il server confrontando i codici stazione, non gli orari o i
	// nomi: le due fonti scrivono gli stessi posti in modi diversi, e un
	// confronto sui nomi qui sbaglierebbe proprio dove serve non sbagliare.
	Chosen bool `json:"chosen,omitempty"`
}

// treno restituisce il viaggio di un treno del tabellone: dove si trova adesso
// e a che ora è passato dalle fermate che ha già servito.
//
// Un treno che ViaggiaTreno non conosce o non traccia non è un errore: è la
// normalità per metà del tabellone, e la risposta lo dice con `tracked: false`
// invece che con un 404 che il client dovrebbe distinguere da un guasto.
func (s *Server) treno(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, err := strconv.Atoi(q.Get("from"))
	if err != nil || from <= 0 {
		errore(w, http.StatusBadRequest, "parametro 'from' mancante o non valido")
		return
	}
	numero := strings.TrimSpace(q.Get("number"))
	if numero == "" {
		errore(w, http.StatusBadRequest, "parametro 'number' mancante")
		return
	}
	arrivals := q.Get("arrivals") == "true"

	// La fermata dove si scende, se la richiesta la nomina: si confronta il
	// codice ViaggiaTreno, che è lo stesso identificatore su entrambi i lati.
	var codiceScelta string
	if v := q.Get("to"); v != "" {
		if to, err := strconv.Atoi(v); err == nil && to > 0 {
			if st := s.catalogo.ByID(to); st != nil {
				codiceScelta = st.VT
			}
		}
	}

	a, err := s.svc.Andamento(r.Context(), from, arrivals, numero)
	if err != nil {
		log.Printf("andamento treno %s da %d: %v", numero, from, err)
		errore(w, http.StatusBadGateway, "andamento non disponibile")
		return
	}

	risposta := map[string]any{"tracked": false}
	if a != nil {
		fermate := make([]fermataJSON, 0, len(a.Fermate))
		for _, f := range a.Fermate {
			voce := fermataJSON{
				Code: f.Codice, Name: f.Nome,
				Scheduled: orario(f.Programmata), Passed: f.Passata,
			}
			// Orario reale e ritardo hanno senso solo dove il treno è passato:
			// sulle fermate future ViaggiaTreno lascia zero, che non è una
			// previsione ma un campo non compilato.
			if f.Passata {
				voce.Actual, voce.Delay = orario(f.Effettiva), f.Ritardo
			}
			if codiceScelta != "" && f.Codice == codiceScelta {
				voce.Chosen = true
			}
			fermate = append(fermate, voce)
		}
		risposta = map[string]any{
			"tracked": a.Stazione != "",
			"delay":   a.Ritardo,
			"lastSeen": map[string]any{
				"station": a.Stazione,
				"time":    orario(a.Ora),
			},
			"stops": fermate,
		}
	}

	body, err := json.Marshal(risposta)
	if err != nil {
		errore(w, http.StatusInternalServerError, "errore interno")
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	scriviJSON(w, r, body, etag(body))
}

func (s *Server) fileStatici() http.Handler {
	srv := http.FileServer(http.FS(s.statici))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Il service worker deve poter essere sostituito subito, altrimenti un
		// aggiornamento resta invisibile finché la cache del browser non scade.
		if path.Base(r.URL.Path) == "sw.js" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		srv.ServeHTTP(w, r)
	})
}

func scriviJSON(w http.ResponseWriter, r *http.Request, body []byte, tag string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("ETag", tag)
	if corrisponde(r.Header.Get("If-None-Match"), tag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(body)
}

func corrisponde(header, tag string) bool {
	for _, v := range strings.Split(header, ",") {
		if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "W/")) == tag {
			return true
		}
	}
	return false
}

func etag(body []byte) string {
	h := sha256.Sum256(body)
	return `"` + hex.EncodeToString(h[:12]) + `"`
}

func errore(w http.ResponseWriter, codice int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(codice)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// comprimi è una gzip minima al posto di una libreria: le risposte sono JSON e
// testo, dove la compressione vale un fattore cinque o più, ed è il guadagno
// principale su una connessione mobile.
func comprimi(next http.Handler) http.Handler {
	pool := sync.Pool{New: func() any { w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed); return w }}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		var buf bytes.Buffer
		gz := pool.Get().(*gzip.Writer)
		defer pool.Put(gz)
		gz.Reset(&buf)

		cw := &scritturaCompressa{ResponseWriter: w, gz: gz}
		next.ServeHTTP(cw, r)

		if !cw.comprime {
			return
		}
		gz.Close()
		h := w.Header()
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		h.Set("Content-Length", strconv.Itoa(buf.Len()))
		w.WriteHeader(http.StatusOK)
		w.Write(buf.Bytes())
	})
}

// scritturaCompressa devia il corpo nel gzip, ma solo quando ne vale la pena e
// non ci sono rischi: una 304 non ha corpo, una 206 è un frammento che il
// client si aspetta intatto, e le immagini sono già compresse. In tutti questi
// casi la risposta prosegue in chiaro verso il client.
type scritturaCompressa struct {
	http.ResponseWriter
	gz       *gzip.Writer
	deciso   bool
	comprime bool
}

func (c *scritturaCompressa) decidi(codice int) {
	if c.deciso {
		return
	}
	c.deciso = true
	c.comprime = codice == http.StatusOK && comprimibile(c.Header().Get("Content-Type"))
	if c.comprime {
		// Si riferirebbe al corpo non compresso; lo riscrive il middleware.
		c.Header().Del("Content-Length")
	} else {
		c.ResponseWriter.WriteHeader(codice)
	}
}

func (c *scritturaCompressa) WriteHeader(codice int) { c.decidi(codice) }

func (c *scritturaCompressa) Write(b []byte) (int, error) {
	// Senza WriteHeader esplicito lo stato è 200 e il Content-Type, se non è
	// stato impostato a mano, a questo punto è già stato dedotto.
	c.decidi(http.StatusOK)
	if !c.comprime {
		return c.ResponseWriter.Write(b)
	}
	return c.gz.Write(b)
}

func comprimibile(contentType string) bool {
	tipo, _, _ := strings.Cut(contentType, ";")
	tipo = strings.TrimSpace(tipo)
	if strings.HasPrefix(tipo, "text/") {
		return true
	}
	switch tipo {
	case "application/json", "application/javascript", "application/manifest+json",
		"image/svg+xml", "application/xml":
		return true
	}
	return false
}
