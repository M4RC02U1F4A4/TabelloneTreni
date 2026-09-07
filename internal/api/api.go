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
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/board"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/stations"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/vt"
)

type Server struct {
	svc      *board.Service
	catalogo *stations.Catalogo
	statici  fs.FS
	versione string
	// statoLinee è la base URL del servizio che segue le linee Trenord. Sta
	// fuori da qui perché interroga Trenord a ritmo suo; il tabellone gli fa
	// solo da tramite, come già fa con RFI, per non obbligare il telefono a
	// conoscere un secondo indirizzo e a farsi bastare il CORS di qualcun altro.
	statoLinee string
	clientHTTP *http.Client

	elencoUnaVolta sync.Once
	elencoBody     []byte
	elencoETag     string
}

func New(svc *board.Service, cat *stations.Catalogo, statici fs.FS, versione, statoLinee string) *Server {
	return &Server{
		svc: svc, catalogo: cat, statici: statici, versione: versione,
		statoLinee: strings.TrimSuffix(statoLinee, "/"),
		clientHTTP: &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stations", s.stazioni)
	mux.HandleFunc("GET /api/board", s.tabellone)
	mux.HandleFunc("GET /api/train", s.treno)
	mux.HandleFunc("GET /api/journey", s.viaggio)
	mux.HandleFunc("GET /api/lines", s.linee)
	mux.HandleFunc("GET /api/lines/notices", s.avvisiLinea)
	mux.HandleFunc("GET /api/push/key", s.inoltraPush("/push/chiave"))
	mux.HandleFunc("POST /api/push/subscribe", s.inoltraPush("/push/abbonamenti"))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	mux.Handle("GET /", s.fileStatici())
	return comprimi(s.dichiaraVersione(mux))
}

// dichiaraVersione firma ogni risposta con la versione del server. Serve alla
// pagina già aperta: su iOS l'app installata resta viva in background per
// giorni, e senza questo indizio continuerebbe a girare col codice di prima
// anche molto dopo il rilascio.
func (s *Server) dichiaraVersione(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Versione", s.versione)
		next.ServeHTTP(w, r)
	})
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
	// Platform è il binario che conta per questa fermata; PlatformScheduled
	// c'è solo quando è cambiato, perché è l'unico caso in cui il previsto è
	// ancora un'informazione — altrimenti sarebbe lo stesso numero due volte.
	Platform          string `json:"platform,omitempty"`
	PlatformScheduled string `json:"platformScheduled,omitempty"`
}

// viaggioJSON è la forma in cui il viaggio di un treno arriva al client, la
// stessa per la scheda aperta da un tabellone e per un treno seguito dalla
// home. Sono lo stesso dato guardato da due porte, e una forma sola vuol dire
// un renderer solo di là.
//
// Un treno che ViaggiaTreno non conosce o non traccia non è un errore: è la
// normalità per metà del tabellone, e la risposta lo dice con `tracked: false`
// invece che con un 404 che il client dovrebbe distinguere da un guasto.
func viaggioJSON(a *vt.Andamento, codiceScelta string) map[string]any {
	if a == nil {
		return map[string]any{"tracked": false}
	}
	fermate := make([]fermataJSON, 0, len(a.Fermate))
	for _, f := range a.Fermate {
		voce := fermataJSON{
			Code: f.Codice, Name: f.Nome,
			Scheduled: orario(f.Programmata), Passed: f.Passata,
			Platform: f.Binario(),
		}
		if f.BinarioCambiato() {
			voce.PlatformScheduled = f.BinarioProgrammato
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
	viaggio := map[string]any{
		"tracked": a.Stazione != "",
		"delay":   a.Ritardo,
		// Le coordinate con cui richiedere questo stesso viaggio. Sono quello
		// che il telefono si salva per seguire il treno: da lì in poi non ha
		// più un tabellone da cui ricavarle.
		"id": map[string]any{
			"origin": a.CodOrigine, "number": a.Numero, "date": a.DataPartenza,
		},
		// Come si chiama il treno. Al tabellone non serve — queste tre cose le
		// ha già stampate — ma una scheda seguita nasce senza tabellone sotto.
		"number":   a.Numero,
		"category": a.Categoria,
		"origin":   a.Origine,
		"terminus": a.Destinazione,
		"arrived":  a.Arrivato,
		// Disrupted dice che sul treno c'è un provvedimento, senza dire quale:
		// vedi vt.Andamento, dove sta il perché di questo silenzio. Serve
		// perché un ritardo sereno su un treno cancellato è la bugia peggiore
		// che questa scheda possa raccontare.
		"disrupted": a.ConProvvedimento,
		"lastSeen": map[string]any{
			"station": a.Stazione,
			"time":    orario(a.Ora),
		},
		"stops": fermate,
	}
	// Quante fermate ViaggiaTreno dichiara soppresse, e solo quando ce ne sono:
	// una mappa non conosce omitempty, e uno zero su ogni treno sano sarebbe un
	// campo che si legge per scoprire che non dice niente.
	if a.FermateSoppresse > 0 {
		viaggio["suppressedStops"] = a.FermateSoppresse
	}
	return viaggio
}

// fermataScelta traduce il PlaceId RFI della stazione dove si scende nel codice
// con cui la stessa stazione compare fra le fermate di ViaggiaTreno. Vuoto se
// la richiesta non la nomina o se le due fonti non si accoppiano lì.
func (s *Server) fermataScelta(v string) string {
	if v == "" {
		return ""
	}
	to, err := strconv.Atoi(v)
	if err != nil || to <= 0 {
		return ""
	}
	if st := s.catalogo.ByID(to); st != nil {
		return st.VT
	}
	return ""
}

// treno restituisce il viaggio di un treno del tabellone: dove si trova adesso
// e a che ora è passato dalle fermate che ha già servito.
//
// Il treno si identifica con il tabellone da cui lo si è aperto, e non con le
// coordinate di ViaggiaTreno: quelle il tabellone le ha già lette, e chiederle
// al client vorrebbe dire fidarsi di quello che rimanda indietro.
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

	a, err := s.svc.Andamento(r.Context(), from, arrivals, numero)
	if err != nil {
		log.Printf("andamento treno %s da %d: %v", numero, from, err)
		errore(w, http.StatusBadGateway, "andamento non disponibile")
		return
	}
	rispondiViaggio(w, r, viaggioJSON(a, s.fermataScelta(q.Get("to"))))
}

// Cosa si accetta come coordinate di un treno seguito.
//
// I codici stazione di ViaggiaTreno sono una lettera e cinque cifre ("S01700");
// il margine in più copre le origini estere, che hanno un prefisso diverso e
// che nessuno qui ha mai enumerato. I numeri di treno sono cifre, con qualche
// suffisso di lettera in giro.
var (
	codiceVT   = regexp.MustCompile(`^[A-Z]{1,2}[0-9]{4,6}$`)
	numeroTren = regexp.MustCompile(`^[0-9A-Za-z]{1,10}$`)
)

// giorniAmmessi è quanto può distare il giorno di partenza dichiarato. Un treno
// seguito è di oggi, al massimo di ieri sera; il margine sta largo perché il
// telefono può avere l'orologio storto, ma resta un margine.
const giorniAmmessi = 3 * 24 * time.Hour

// viaggio restituisce il viaggio di un treno seguito, che si identifica con le
// coordinate di ViaggiaTreno e non con un tabellone.
//
// È l'eccezione alla regola dell'handler qui sopra, e ha un motivo: chi segue
// un treno lo guarda quasi sempre mentre ci è sopra, cioè quando il treno è
// partito e dal tabellone della stazione di partenza è sparito. Ricavare lì le
// coordinate non funzionerebbe proprio nel momento per cui la funzione esiste,
// quindi le tiene il telefono e qui se ne controlla la forma.
//
// Il controllo non serve a stabilire che il treno esista — a quello risponde
// ViaggiaTreno con un corpo vuoto — ma a fare in modo che quello che arriva da
// fuori non possa comporre un indirizzo diverso da quello previsto.
func (s *Server) viaggio(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	origine := strings.ToUpper(strings.TrimSpace(q.Get("origin")))
	numero := strings.TrimSpace(q.Get("number"))
	if !codiceVT.MatchString(origine) || !numeroTren.MatchString(numero) {
		errore(w, http.StatusBadRequest, "treno non valido")
		return
	}
	data, err := strconv.ParseInt(q.Get("date"), 10, 64)
	if err != nil {
		errore(w, http.StatusBadRequest, "parametro 'date' mancante o non valido")
		return
	}
	if scarto := time.Since(time.UnixMilli(data)); scarto > giorniAmmessi || scarto < -giorniAmmessi {
		errore(w, http.StatusBadRequest, "giorno di partenza fuori intervallo")
		return
	}

	a, err := s.svc.Viaggio(r.Context(), origine, numero, data)
	if err != nil {
		log.Printf("viaggio treno %s da %s del %d: %v", numero, origine, data, err)
		errore(w, http.StatusBadGateway, "andamento non disponibile")
		return
	}
	rispondiViaggio(w, r, viaggioJSON(a, s.fermataScelta(q.Get("to"))))
}

func rispondiViaggio(w http.ResponseWriter, r *http.Request, risposta map[string]any) {
	body, err := json.Marshal(risposta)
	if err != nil {
		errore(w, http.StatusInternalServerError, "errore interno")
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	scriviJSON(w, r, body, etag(body))
}

// linee inoltra lo stato delle linee dal servizio che lo segue.
//
// Il corpo si legge tutto in memoria invece di riversarlo: sono pochi KB, e
// averlo intero permette di calcolarci l'ETag, che è quello che risparmia il
// trasferimento quando i bollini non cambiano — cioè quasi sempre.
func (s *Server) linee(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, s.statoLinee+"/linee", nil)
	if err != nil {
		errore(w, http.StatusInternalServerError, "errore interno")
		return
	}
	resp, err := s.clientHTTP.Do(req)
	if err != nil {
		log.Printf("stato linee: %v", err)
		errore(w, http.StatusBadGateway, "stato linee non disponibile")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("stato linee: risposta %s", resp.Status)
		errore(w, http.StatusBadGateway, "stato linee non disponibile")
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		errore(w, http.StatusBadGateway, "stato linee non disponibile")
		return
	}
	// I bollini li muove una persona in sala operativa: cambiano di rado, ma
	// quando cambiano vanno visti subito, quindi si richiede sempre e si
	// risparmia solo il corpo.
	w.Header().Set("Cache-Control", "no-cache")
	scriviJSON(w, r, body, etag(body))
}

// codiceLinea limita quello che si accetta come nome di linea prima di
// rilanciarlo al servizio: i codici veri sono lettere, cifre e underscore.
var codiceLinea = regexp.MustCompile(`^[A-Za-z0-9_]{1,10}$`)

// avvisiLinea chiede al servizio le comunicazioni di una linea. Le si prende a
// richiesta e non insieme all'elenco perché il dettaglio pesa, e chi apre una
// riga ne apre una, non sessantacinque.
func (s *Server) avvisiLinea(w http.ResponseWriter, r *http.Request) {
	linea := r.URL.Query().Get("line")
	if !codiceLinea.MatchString(linea) {
		errore(w, http.StatusBadRequest, "parametro 'line' mancante o non valido")
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet,
		s.statoLinee+"/avvisi?linea="+url.QueryEscape(linea), nil)
	if err != nil {
		errore(w, http.StatusInternalServerError, "errore interno")
		return
	}
	resp, err := s.clientHTTP.Do(req)
	if err != nil {
		log.Printf("avvisi linea %s: %v", linea, err)
		errore(w, http.StatusBadGateway, "avvisi non disponibili")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		errore(w, http.StatusBadGateway, "avvisi non disponibili")
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		errore(w, http.StatusBadGateway, "avvisi non disponibili")
		return
	}
	// Le comunicazioni cambiano di rado ma quando cambiano contano: si chiede
	// sempre, e l'ETag risparmia il corpo quando sono le stesse.
	w.Header().Set("Cache-Control", "no-cache")
	scriviJSON(w, r, body, etag(body))
}

// inoltraPush passa al servizio delle linee le due richieste che riguardano le
// notifiche. Il telefono parla con una sola origine — la stessa ragione per cui
// questo server esiste — e il servizio resta senza porte pubblicate.
func (s *Server) inoltraPush(percorso string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Il corpo di chi si abbona contiene le sue chiavi push: si limita a
		// una misura ragionevole prima di rilanciarlo, non dopo.
		var corpo io.Reader
		if r.Body != nil {
			corpo = io.LimitReader(r.Body, 64<<10)
		}
		req, err := http.NewRequestWithContext(r.Context(), r.Method, s.statoLinee+percorso, corpo)
		if err != nil {
			errore(w, http.StatusInternalServerError, "errore interno")
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := s.clientHTTP.Do(req)
		if err != nil {
			log.Printf("notifiche %s: %v", percorso, err)
			errore(w, http.StatusBadGateway, "notifiche non disponibili")
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		// Le notifiche non si mettono mai in cache da nessuna parte: la chiave
		// cambia solo con la configurazione, ma un abbonamento servito da una
		// cache sarebbe un abbonamento mai arrivato.
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, io.LimitReader(resp.Body, 1<<20))
	}
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
