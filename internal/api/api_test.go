package api

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/board"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/rfi"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/stations"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/vt"
)

type sorgenteFinta struct{}

func (sorgenteFinta) Fetch(ctx context.Context, placeID int, arrivals bool) (*rfi.Board, error) {
	return &rfi.Board{
		PlaceID: placeID, Station: "PROVA", Arrivals: arrivals,
		Trains: []rfi.Train{{Number: "1", Time: "10:00", Terminus: "ALTROVE"}},
	}, nil
}

func server() http.Handler { return serverCon("") }

func serverCon(statoLinee string) http.Handler {
	statici := fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>x</title>" + string(make([]byte, 2000)))},
		"icona-180.png": {Data: []byte("\x89PNG\r\n\x1a\n" + string(make([]byte, 2000)))},
	}
	return New(board.New(sorgenteFinta{}, stations.Default), stations.Default, statici, "test", statoLinee).Handler()
}

func chiedi(t *testing.T, h http.Handler, percorso string, intestazioni map[string]string) *http.Response {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, percorso, nil)
	for k, v := range intestazioni {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Result()
}

func TestJSONCompresso(t *testing.T) {
	resp := chiedi(t, server(), "/api/board?from=1715", map[string]string{"Accept-Encoding": "gzip"})
	if resp.StatusCode != 200 {
		t.Fatalf("stato %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("risposta non compressa: %v", resp.Header)
	}
	// Deve essere gzip valido: un corpo compresso a metà passerebbe comunque
	// il controllo sull'intestazione.
	z, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	corpo, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if len(corpo) == 0 {
		t.Fatal("corpo vuoto")
	}
}

// Comprimere un PNG non guadagna niente e complica la risposta: la
// compressione deve valere solo per i tipi testuali.
func TestBinariNonCompressi(t *testing.T) {
	resp := chiedi(t, server(), "/icona-180.png", map[string]string{"Accept-Encoding": "gzip"})
	if resp.Header.Get("Content-Encoding") == "gzip" {
		t.Error("il PNG è stato compresso")
	}
	if resp.StatusCode != 200 {
		t.Errorf("stato %d", resp.StatusCode)
	}
}

// Il client si aggiorna ogni minuto ma il tabellone cambia più di rado: la
// 304 è ciò che rende quasi gratuito quel giro.
func TestNonModificato(t *testing.T) {
	h := server()
	primo := chiedi(t, h, "/api/board?from=1715", nil)
	tag := primo.Header.Get("ETag")
	if tag == "" {
		t.Fatal("nessun ETag")
	}
	secondo := chiedi(t, h, "/api/board?from=1715", map[string]string{"If-None-Match": tag})
	if secondo.StatusCode != http.StatusNotModified {
		t.Fatalf("stato %d, attesa 304", secondo.StatusCode)
	}
	corpo, _ := io.ReadAll(secondo.Body)
	if len(corpo) != 0 {
		t.Errorf("la 304 ha un corpo di %d byte", len(corpo))
	}
	// Anche con gzip richiesto, una 304 non deve dichiararsi compressa.
	terzo := chiedi(t, h, "/api/board?from=1715",
		map[string]string{"If-None-Match": tag, "Accept-Encoding": "gzip"})
	if terzo.StatusCode != http.StatusNotModified {
		t.Fatalf("con gzip: stato %d, attesa 304", terzo.StatusCode)
	}
	if terzo.Header.Get("Content-Encoding") != "" {
		t.Error("la 304 dichiara una codifica")
	}
}

func TestParametriNonValidi(t *testing.T) {
	h := server()
	for _, p := range []string{"/api/board", "/api/board?from=abc", "/api/board?from=1715&to=-3"} {
		if resp := chiedi(t, h, p, nil); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: stato %d, attesa 400", p, resp.StatusCode)
		}
	}
}

func TestElencoStazioni(t *testing.T) {
	resp := chiedi(t, server(), "/api/stations", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("stato %d", resp.StatusCode)
	}
	corpo, _ := io.ReadAll(resp.Body)
	if len(corpo) < 10000 {
		t.Errorf("elenco di soli %d byte", len(corpo))
	}
}

// Un treno che ViaggiaTreno non segue non è un errore: è la normalità per metà
// del tabellone, e il client deve poterlo distinguere da un guasto senza
// interpretare un codice di stato.
func TestTrenoNonSeguito(t *testing.T) {
	res := chiedi(t, server(), "/api/train?from=1715&number=1", nil)
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("stato = %d, atteso 200", res.StatusCode)
	}
	var d map[string]any
	if err := json.NewDecoder(res.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d["tracked"] != false {
		t.Errorf("tracked = %v, atteso false", d["tracked"])
	}
}

func TestTrenoParametriMancanti(t *testing.T) {
	casi := []struct {
		nome     string
		percorso string
	}{
		{"senza stazione", "/api/train?number=1"},
		{"stazione non numerica", "/api/train?from=abc&number=1"},
		{"stazione a zero", "/api/train?from=0&number=1"},
		{"senza numero di treno", "/api/train?from=1715"},
		{"numero di soli spazi", "/api/train?from=1715&number=%20%20"},
	}
	for _, caso := range casi {
		t.Run(caso.nome, func(t *testing.T) {
			res := chiedi(t, server(), caso.percorso, nil)
			defer res.Body.Close()
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("stato = %d, atteso 400", res.StatusCode)
			}
		})
	}
}

// La pagina già aperta si accorge di un rilascio solo da questa intestazione:
// se sparisse, l'app installata su iOS resterebbe indietro senza dirlo.
func TestVersioneNelleIntestazioni(t *testing.T) {
	h := server()
	for _, p := range []string{"/api/board?from=1715", "/index.html"} {
		if v := chiedi(t, h, p, nil).Header.Get("X-Versione"); v != "test" {
			t.Errorf("%s: X-Versione = %q", p, v)
		}
	}
}

// Il tabellone fa da tramite verso il servizio delle linee per la stessa
// ragione per cui esiste: il telefono parla con un'origine sola. Qui conta che
// il corpo passi intatto e che l'ETag venga calcolato, perché è quello che
// risparmia il trasferimento quando i bollini non cambiano.
func TestLineeInoltrate(t *testing.T) {
	const corpo = `{"updated":"2026-09-07T10:00:00Z","lines":[{"code":"S2","name":"Seveso","group":"LINEE SUBURBANE","status":1}]}`
	monte := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/linee" {
			t.Errorf("percorso richiesto = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, corpo)
	}))
	defer monte.Close()

	h := serverCon(monte.URL)
	resp := chiedi(t, h, "/api/lines", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stato = %d", resp.StatusCode)
	}
	letto, _ := io.ReadAll(resp.Body)
	if string(letto) != corpo {
		t.Fatalf("corpo alterato:\n  ho  %s\n  atteso %s", letto, corpo)
	}
	tag := resp.Header.Get("ETag")
	if tag == "" {
		t.Fatal("senza ETag: il corpo si ritrasferirebbe a ogni richiesta")
	}

	// Con l'ETag di ritorno il corpo non deve ripartire.
	ancora := chiedi(t, h, "/api/lines", map[string]string{"If-None-Match": tag})
	defer ancora.Body.Close()
	if ancora.StatusCode != http.StatusNotModified {
		t.Fatalf("stato = %d, atteso 304", ancora.StatusCode)
	}
}

// Se il servizio delle linee è giù, il tabellone deve dirlo e restare in
// piedi: è la parte che serve davvero a prendere il treno.
func TestLineeServizioGiu(t *testing.T) {
	monte := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rotto", http.StatusInternalServerError)
	}))
	monte.Close() // chiuso apposta: la connessione non si apre nemmeno

	resp := chiedi(t, serverCon(monte.URL), "/api/lines", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("stato = %d, atteso 502", resp.StatusCode)
	}
	var d map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d["error"] == "" {
		t.Errorf("risposta senza errore leggibile: %v", d)
	}

	// Il resto del tabellone continua a funzionare.
	if r := chiedi(t, serverCon(monte.URL), "/api/board?from=1715", nil); r.StatusCode != 200 {
		t.Errorf("tabellone = %d", r.StatusCode)
	}
}

// Le due richieste delle notifiche passano dal tabellone perché il telefono
// parla con una sola origine. Qui conta che arrivino al percorso giusto del
// servizio, che il corpo passi intatto e che la risposta non finisca in
// nessuna cache: un abbonamento servito da una cache è un abbonamento mai
// arrivato.
func TestPushInoltrata(t *testing.T) {
	var visti []string
	var corpi []string
	monte := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		visti = append(visti, r.Method+" "+r.URL.Path)
		b, _ := io.ReadAll(r.Body)
		corpi = append(corpi, string(b))
		if r.URL.Path == "/push/chiave" {
			io.WriteString(w, `{"key":"BLzNaPs"}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer monte.Close()
	h := serverCon(monte.URL)

	chiave := chiedi(t, h, "/api/push/key", nil)
	defer chiave.Body.Close()
	if chiave.StatusCode != http.StatusOK {
		t.Fatalf("chiave: stato = %d", chiave.StatusCode)
	}
	if c := chiave.Header.Get("Cache-Control"); c != "no-store" {
		t.Errorf("Cache-Control = %q, atteso no-store", c)
	}

	const abbonamento = `{"subscription":{"endpoint":"https://push.example/x"},"lines":["S2"]}`
	r := httptest.NewRequest(http.MethodPost, "/api/push/subscribe", strings.NewReader(abbonamento))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("abbonamento: stato = %d", w.Code)
	}

	if len(visti) != 2 || visti[0] != "GET /push/chiave" || visti[1] != "POST /push/abbonamenti" {
		t.Fatalf("richieste al servizio = %v", visti)
	}
	if corpi[1] != abbonamento {
		t.Errorf("corpo alterato:\n  ho  %s\n  atteso %s", corpi[1], abbonamento)
	}
}

// Se il servizio delle linee è giù, abbonarsi deve fallire in modo leggibile
// invece di sembrare riuscito.
func TestPushServizioGiu(t *testing.T) {
	monte := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	monte.Close()

	resp := chiedi(t, serverCon(monte.URL), "/api/push/key", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("stato = %d, atteso 502", resp.StatusCode)
	}
}

// Le comunicazioni di una linea si chiedono a richiesta, quando qualcuno apre
// la riga: il tabellone fa da tramite e passa il codice al servizio.
func TestAvvisiLineaInoltrati(t *testing.T) {
	var visto string
	monte := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		visto = r.URL.String()
		io.WriteString(w, `{"notices":[{"date":"2026-09-01T16:29:27Z","text":"sciopero"}]}`)
	}))
	defer monte.Close()

	resp := chiedi(t, serverCon(monte.URL), "/api/lines/notices?line=RE_13", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stato = %d", resp.StatusCode)
	}
	if visto != "/avvisi?linea=RE_13" {
		t.Errorf("richiesto %q", visto)
	}
	if resp.Header.Get("ETag") == "" {
		t.Error("senza ETag: il corpo si ritrasferirebbe a ogni apertura")
	}
}

// Il codice arriva da fuori e finisce in una richiesta verso Trenord: quello
// che non è un codice di linea non deve nemmeno partire.
func TestAvvisiLineaCodiceRifiutato(t *testing.T) {
	monte := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("non doveva essere chiamato: %s", r.URL)
	}))
	defer monte.Close()
	h := serverCon(monte.URL)

	for _, caso := range []string{"", "S2%20OR%201=1", "../../etc", "S2/../altro", strings.Repeat("S", 30)} {
		resp := chiedi(t, h, "/api/lines/notices?line="+caso, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%q: stato = %d, atteso 400", caso, resp.StatusCode)
		}
	}
}

// Un treno seguito non ha più un tabellone da cui ricavare le coordinate: le
// manda il telefono, e qui se ne controlla la forma. Il controllo non stabilisce
// che il treno esista — a quello risponde ViaggiaTreno — ma che quello che
// arriva da fuori non possa comporre un indirizzo diverso da quello previsto.
func TestViaggioCoordinateRifiutate(t *testing.T) {
	oggi := strconv.FormatInt(time.Now().UnixMilli(), 10)
	casi := []struct {
		nome     string
		percorso string
	}{
		{"senza origine", "/api/journey?number=2247&date=" + oggi},
		{"origine con una barra", "/api/journey?origin=S01700%2F..%2Fx&number=2247&date=" + oggi},
		{"origine troppo lunga", "/api/journey?origin=S0170012345&number=2247&date=" + oggi},
		{"origine con punti", "/api/journey?origin=..&number=2247&date=" + oggi},
		{"senza numero", "/api/journey?origin=S01700&date=" + oggi},
		{"numero con punteggiatura", "/api/journey?origin=S01700&number=22%2F47&date=" + oggi},
		{"senza giorno", "/api/journey?origin=S01700&number=2247"},
		{"giorno non numerico", "/api/journey?origin=S01700&number=2247&date=ieri"},
		{"giorno della settimana scorsa", "/api/journey?origin=S01700&number=2247&date=1"},
	}
	for _, caso := range casi {
		t.Run(caso.nome, func(t *testing.T) {
			res := chiedi(t, server(), caso.percorso, nil)
			defer res.Body.Close()
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("stato = %d, atteso 400", res.StatusCode)
			}
		})
	}
}

// Coordinate in ordine passano, e un treno che nessuno segue esce con la stessa
// risposta della scheda aperta da un tabellone: non è un errore, è un silenzio.
func TestViaggioCoordinateAccettate(t *testing.T) {
	oggi := strconv.FormatInt(time.Now().UnixMilli(), 10)
	res := chiedi(t, server(), "/api/journey?origin=S01700&number=2247&date="+oggi, nil)
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("stato = %d, atteso 200", res.StatusCode)
	}
	var d map[string]any
	if err := json.NewDecoder(res.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d["tracked"] != false {
		t.Errorf("tracked = %v, atteso false", d["tracked"])
	}
}

// Il serializzatore è il posto dove queste due cose si decidono, e si prova
// direttamente: montare una finta ViaggiaTreno sotto al server per leggere due
// campi vorrebbe dire provare il cablaggio invece della regola.
func TestIlProvvedimentoArrivaAlClient(t *testing.T) {
	sano := viaggioJSON(&vt.Andamento{Stazione: "PROVA"}, "")
	if sano["disrupted"] != false {
		t.Errorf("treno sano: disrupted = %v, atteso false", sano["disrupted"])
	}
	// Uno zero su ogni treno sano sarebbe un campo che si legge per scoprire
	// che non dice niente: quando non ce ne sono, non c'è.
	if _, c := sano["suppressedStops"]; c {
		t.Error("treno sano: suppressedStops presente")
	}

	guasto := viaggioJSON(&vt.Andamento{
		Stazione: "PROVA", ConProvvedimento: true, FermateSoppresse: 2,
	}, "")
	if guasto["disrupted"] != true {
		t.Errorf("disrupted = %v, atteso true", guasto["disrupted"])
	}
	if guasto["suppressedStops"] != 2 {
		t.Errorf("suppressedStops = %v, atteso 2", guasto["suppressedStops"])
	}
}

// La fermata dove si scende si accende su una sola riga, e per codice: i nomi
// delle due fonti non coincidono, ed è qui che non devono sbagliare.
func TestSoloLaFermataSceltaSiAccende(t *testing.T) {
	a := &vt.Andamento{Stazione: "PROVA", Fermate: []vt.Fermata{
		{Codice: "S01700", Nome: "UNO"},
		{Codice: "S01645", Nome: "DUE"},
		{Codice: "S01820", Nome: "TRE"},
	}}
	fermate, ok := viaggioJSON(a, "S01645")["stops"].([]fermataJSON)
	if !ok {
		t.Fatal("le fermate non sono nella forma attesa")
	}
	for _, f := range fermate {
		if atteso := f.Code == "S01645"; f.Chosen != atteso {
			t.Errorf("%s: chosen = %v, atteso %v", f.Name, f.Chosen, atteso)
		}
	}

	// Nessuna scelta: nessuna accesa, che è il caso del tabellone senza filtro.
	fermate, _ = viaggioJSON(a, "")["stops"].([]fermataJSON)
	for _, f := range fermate {
		if f.Chosen {
			t.Errorf("%s accesa senza fermata scelta", f.Name)
		}
	}
}

/* ---------- avvisi di stazione ---------- */

// Una sorgente che mette un avviso solo su alcune stazioni e conta le pagine
// chieste: gli avvisi vanno letti dal tabellone delle partenze, e RFI non deve
// vedere una pagina per ogni volta che qualcuno apre la home.
type sorgenteAvvisi struct {
	mu    sync.Mutex
	letti map[int]int
	muti  map[int]bool // stazioni senza avvisi
}

func (s *sorgenteAvvisi) Fetch(ctx context.Context, placeID int, arrivals bool) (*rfi.Board, error) {
	s.mu.Lock()
	if s.letti == nil {
		s.letti = map[int]int{}
	}
	s.letti[placeID]++
	s.mu.Unlock()

	b := &rfi.Board{
		PlaceID: placeID, Station: fmt.Sprintf("STAZIONE %d", placeID), Arrivals: arrivals,
		Trains: []rfi.Train{{Number: "1", Time: "10:00", Terminus: "ALTROVE"}},
	}
	if !s.muti[placeID] {
		b.Notices = []string{fmt.Sprintf("ASCENSORE FUORI SERVIZIO A %d", placeID)}
	}
	return b, nil
}

func (s *sorgenteAvvisi) volte(placeID int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.letti[placeID]
}

func serverAvvisi(src *sorgenteAvvisi) (*Server, http.Handler) {
	srv := New(board.New(src, stations.Default), stations.Default, fstest.MapFS{}, "test", "")
	return srv, srv.Handler()
}

type rispostaAvvisi struct {
	Stations []struct {
		PlaceID int      `json:"placeId"`
		Station string   `json:"station"`
		Notices []string `json:"notices"`
	} `json:"stations"`
}

func leggiAvvisi(t *testing.T, h http.Handler, percorso string) rispostaAvvisi {
	t.Helper()
	res := chiedi(t, h, percorso, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%s: stato = %d", percorso, res.StatusCode)
	}
	var d rispostaAvvisi
	if err := json.NewDecoder(res.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	return d
}

// Le stazioni tornano nell'ordine della richiesta, con dentro solo quelle che
// hanno qualcosa da dire: un banner senza testo non si disegna, e farlo
// decidere al client vorrebbe dire mandargli quasi sempre voci vuote.
func TestAvvisiStazioni(t *testing.T) {
	src := &sorgenteAvvisi{muti: map[int]bool{1728: true}}
	_, h := serverAvvisi(src)

	// 830 non esiste nel catalogo: si salta senza far fallire il resto.
	d := leggiAvvisi(t, h, "/api/notices?stations=1728,1715,830")
	if len(d.Stations) != 1 {
		t.Fatalf("stazioni con avvisi = %d, attesa 1: %+v", len(d.Stations), d.Stations)
	}
	s := d.Stations[0]
	if s.PlaceID != 1715 || s.Station != "STAZIONE 1715" {
		t.Errorf("stazione = %+v", s)
	}
	if len(s.Notices) != 1 || s.Notices[0] != "ASCENSORE FUORI SERVIZIO A 1715" {
		t.Errorf("avvisi = %q", s.Notices)
	}
	if src.volte(830) != 0 {
		t.Errorf("la stazione sconosciuta è stata chiesta a RFI %d volte", src.volte(830))
	}
	// Gli avvisi si prendono dal tabellone delle partenze: il verso conta,
	// perché arrivi e partenze della stessa stazione ne portano di diversi.
	if src.volte(1715) != 1 {
		t.Errorf("pagine chieste per 1715 = %d, attesa 1", src.volte(1715))
	}

	// L'ordine è quello della query, non quello in cui RFI risponde.
	src2 := &sorgenteAvvisi{}
	_, h2 := serverAvvisi(src2)
	d2 := leggiAvvisi(t, h2, "/api/notices?stations=1728,1715")
	if len(d2.Stations) != 2 || d2.Stations[0].PlaceID != 1728 || d2.Stations[1].PlaceID != 1715 {
		t.Errorf("ordine = %+v", d2.Stations)
	}
}

// La home si aggiorna una volta al minuto e ogni buco costa a RFI una pagina
// da ~280 KB: due letture di fila non devono diventare due richieste.
func TestAvvisiInCache(t *testing.T) {
	src := &sorgenteAvvisi{}
	srv, h := serverAvvisi(src)

	primo := leggiAvvisi(t, h, "/api/notices?stations=1715")
	secondo := leggiAvvisi(t, h, "/api/notices?stations=1715")
	if len(primo.Stations) != 1 || len(secondo.Stations) != 1 {
		t.Fatalf("risposte = %+v / %+v", primo.Stations, secondo.Stations)
	}
	if n := src.volte(1715); n != 1 {
		t.Errorf("pagine chieste a RFI = %d, attesa 1", n)
	}
	// La cache è quella degli avvisi e non quella dei tabelloni, che dura solo
	// trenta secondi: se un domani sparisse, la scadenza lo direbbe.
	v, presente := srv.avvisi[1715]
	if !presente {
		t.Fatal("niente in cache per 1715")
	}
	if d := time.Until(v.scadeIl); d < avvisiTTL-time.Minute || d > avvisiTTL {
		t.Errorf("scadenza fra %v, atteso circa %v", d, avvisiTTL)
	}
}

// Il parametro arriva da fuori. Un elenco che non è un elenco di numeri è un
// errore del chiamante, non una risposta vuota da interpretare.
func TestAvvisiParametriNonValidi(t *testing.T) {
	_, h := serverAvvisi(&sorgenteAvvisi{})
	casi := []string{
		"/api/notices",
		"/api/notices?stations=",
		"/api/notices?stations=abc",
		"/api/notices?stations=1715,x",
		"/api/notices?stations=-1",
		"/api/notices?stations=0",
		"/api/notices?stations=1,2,3,4,5,6,7,8,9",
	}
	for _, p := range casi {
		res := chiedi(t, h, p, nil)
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: stato = %d, attesa 400", p, res.StatusCode)
		}
	}
}

// Una stazione che non risponde non deve far fallire la richiesta: il banner è
// decorazione, e le altre stazioni hanno comunque i loro avvisi.
func TestAvvisiStazioneGuasta(t *testing.T) {
	// stations.Default non contiene l'id 999999: board.Service lo rifiuta come
	// sconosciuto, che è lo stesso errore che darebbe RFI irraggiungibile.
	src := &sorgenteAvvisi{}
	srv := New(board.New(src, stations.Default), catalogoConFinta(t), fstest.MapFS{}, "test", "")

	d := leggiAvvisi(t, srv.Handler(), "/api/notices?stations=999999,1715")
	if len(d.Stations) != 1 || d.Stations[0].PlaceID != 1715 {
		t.Fatalf("stazioni = %+v", d.Stations)
	}
}

// Un catalogo che conosce una stazione in più di quello di board.Service: è il
// modo di far fallire la lettura di una sola stazione senza toccare la rete.
func catalogoConFinta(t *testing.T) *stations.Catalogo {
	t.Helper()
	c, err := stations.Load([]byte(`{"generated":"2026-09-08","stations":[{"i":999999,"n":"FINTA"},{"i":1715,"n":"MILANO PORTA GARIBALDI"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
