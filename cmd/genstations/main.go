// Comando genstations ricostruisce internal/stations/stations.json.
//
// Gira a mano, non in produzione: il catalogo cambia una volta ogni tanto e
// tenerlo committato evita al server sia un'attesa in avvio sia una dipendenza
// da due siti esterni per poter semplicemente partire.
//
//	go run ./cmd/genstations
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/stations"
	"golang.org/x/net/html"
)

const (
	urlRFI = "https://iechub.rfi.it/ArriviPartenze"
	urlVT  = baseVT + "elencoStazioni/"
	baseVT = "http://www.viaggiatreno.it/infomobilita/resteasy/viaggiatreno/"
)

// aliasManuali copre le fermate che nessun incrocio automatico risolve, perché
// la grafia del tabellone non somiglia abbastanza a nessuno dei nomi noti.
// Chiave: PlaceId RFI. Aggiungerne uno è il rimedio quando un treno che ferma
// dove dici tu non compare nei risultati filtrati.
var aliasManuali = map[int][]string{
	// Il tabellone la chiama con una parola in più del suo stesso catalogo.
	3098: {"RHO FIERA MILANO"},
}

func main() {
	out := flag.String("o", "internal/stations/stations.json", "file da scrivere")
	soloCoord := flag.Bool("coordinate", false,
		"non rigenerare il catalogo: aggiungi le coordinate mancanti a quello che c'è")
	flag.Parse()

	if *soloCoord {
		if err := aggiungiCoordinate(*out); err != nil {
			log.Fatal(err)
		}
		return
	}

	rfi, err := scaricaRFI()
	if err != nil {
		log.Fatalf("elenco RFI: %v", err)
	}
	log.Printf("RFI: %d stazioni", len(rfi))

	vt, err := scaricaViaggiaTreno()
	if err != nil {
		// Senza ViaggiaTreno il catalogo si genera lo stesso, solo con meno
		// alias: il filtro per destinazione ne esce indebolito, non rotto.
		log.Printf("attenzione: ViaggiaTreno non raggiungibile (%v), niente alias", err)
	}
	log.Printf("ViaggiaTreno: %d stazioni", len(vt))

	elenco := unisci(rfi, vt)

	body, err := json.Marshal(struct {
		Generated string              `json:"generated"`
		Stations  []*stations.Station `json:"stations"`
	}{time.Now().UTC().Format(time.RFC3339), elenco})
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, body, 0o644); err != nil {
		log.Fatal(err)
	}

	conAlias, conCodice := 0, 0
	for _, s := range elenco {
		if len(s.Aliases) > 0 {
			conAlias++
		}
		if s.VT != "" {
			conCodice++
		}
	}
	fmt.Printf("scritte %d stazioni in %s (%d con alias, %d col codice ViaggiaTreno, %d KB)\n",
		len(elenco), *out, conAlias, conCodice, len(body)/1024)
}

/*
aggiungiCoordinate arricchisce il catalogo che c'è già, invece di rifarlo.

	ViaggiaTreno le pubblica una stazione per volta e in due passaggi — prima la
	regione, poi il dettaglio — quindi sono un paio di migliaia di richieste: non
	è roba da rifare a ogni giro, e soprattutto non è roba per cui valga la pena
	ricostruire anche il resto del catalogo, che verrebbe da una lettura nuova di
	RFI e cambierebbe cose che nessuno ha chiesto di cambiare.

	Riprende da dove si era fermato: chiede solo le stazioni che hanno un codice
	ViaggiaTreno e non hanno ancora le coordinate. Rilanciarlo dopo
	un'interruzione costa solo quello che manca.
*/
func aggiungiCoordinate(percorso string) error {
	raw, err := os.ReadFile(percorso)
	if err != nil {
		return err
	}
	var f struct {
		Generated string              `json:"generated"`
		Stations  []*stations.Station `json:"stations"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return err
	}

	var mancanti []*stations.Station
	for _, s := range f.Stations {
		if s.VT != "" && s.Lat == 0 && s.Lon == 0 {
			mancanti = append(mancanti, s)
		}
	}
	log.Printf("catalogo: %d stazioni, %d da chiedere", len(f.Stations), len(mancanti))
	if len(mancanti) == 0 {
		return nil
	}

	// Poche alla volta e con una pausa: è il servizio di qualcun altro, e
	// chiedergli duemila volte di fila il più in fretta possibile è il modo di
	// farsi chiudere la porta a metà lavoro.
	const paralleli = 4
	cli := &http.Client{Timeout: 20 * time.Second}
	sem := make(chan struct{}, paralleli)
	var mu sync.Mutex
	var fatte, falliti int
	var wg sync.WaitGroup

	for i, st := range mancanti {
		wg.Add(1)
		go func(i int, st *stations.Station) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			time.Sleep(time.Duration(i%paralleli) * 120 * time.Millisecond)

			lat, lon, err := coordinateDi(cli, st.VT)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				falliti++
				if falliti <= 5 {
					log.Printf("%s (%s): %v", st.Name, st.VT, err)
				}
				return
			}
			st.Lat, st.Lon = lat, lon
			fatte++
			if fatte%200 == 0 {
				log.Printf("… %d/%d", fatte, len(mancanti))
			}
		}(i, st)
	}
	wg.Wait()

	body, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if err := os.WriteFile(percorso, body, 0o644); err != nil {
		return err
	}
	conCoord := 0
	for _, s := range f.Stations {
		if s.Lat != 0 {
			conCoord++
		}
	}
	fmt.Printf("coordinate: %d nuove, %d non riuscite — %d stazioni su %d ora ce le hanno (%d KB)\n",
		fatte, falliti, conCoord, len(f.Stations), len(body)/1024)
	return nil
}

// coordinateDi legge lat/lon di una stazione. Servono due richieste: il
// dettaglio vuole il numero della regione, che si scopre solo chiedendolo — con
// un numero sbagliato risponde vuoto invece di sbagliare, cioè nel modo più
// scomodo possibile.
func coordinateDi(cli *http.Client, codiceVT string) (float64, float64, error) {
	reg, err := prendiTesto(cli, baseVT+"regione/"+codiceVT)
	if err != nil {
		return 0, 0, fmt.Errorf("regione: %w", err)
	}
	reg = strings.TrimSpace(reg)
	if reg == "" {
		return 0, 0, fmt.Errorf("regione vuota")
	}
	corpo, err := prendiTesto(cli, baseVT+"dettaglioStazione/"+codiceVT+"/"+reg)
	if err != nil {
		return 0, 0, fmt.Errorf("dettaglio: %w", err)
	}
	var d struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	}
	if err := json.Unmarshal([]byte(corpo), &d); err != nil {
		return 0, 0, fmt.Errorf("dettaglio illeggibile")
	}
	// Lo zero non è un posto: è il campo non compilato, e va trattato come un
	// fallimento invece che scritto nel catalogo.
	if d.Lat == 0 || d.Lon == 0 {
		return 0, 0, fmt.Errorf("senza coordinate")
	}
	return d.Lat, d.Lon, nil
}

func prendiTesto(cli *http.Client, u string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "TabelloneTreni/genstations")
	resp, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return string(b), err
}

// scaricaRFI estrae le coppie PlaceId/nome dalla <select> della home. È l'unica
// fonte possibile: la ricerca stazione del sito RFI è interamente lato client,
// quindi non esiste alcun endpoint da interrogare.
func scaricaRFI() (map[int]string, error) {
	doc, err := prendiHTML(urlRFI)
	if err != nil {
		return nil, err
	}
	out := map[int]string{}
	var visita func(*html.Node, bool)
	visita = func(n *html.Node, dentroSelect bool) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "select":
				dentroSelect = attr(n, "id") == "ElencoLocalita"
			case "option":
				if dentroSelect {
					if id, err := strconv.Atoi(attr(n, "value")); err == nil && id > 0 {
						if nome := strings.TrimSpace(testo(n)); nome != "" {
							out[id] = nome
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visita(c, dentroSelect)
		}
	}
	visita(doc, false)
	if len(out) == 0 {
		return nil, fmt.Errorf("nessuna <option> trovata: la home RFI è cambiata")
	}
	return out, nil
}

type stazioneVT struct {
	CodiceStazione string `json:"codiceStazione"`
	Localita       struct {
		NomeLungo string `json:"nomeLungo"`
		NomeBreve string `json:"nomeBreve"`
	} `json:"localita"`
}

// datiVT è quello che di ViaggiaTreno finisce nel catalogo.
type datiVT struct {
	// Breve è la forma abbreviata che RFI stampa nelle fermate
	// ("MI BOVISA P.", "MI.P.GARIBALDI"): è il ponte fra le due grafie.
	Breve string
	// Codice è l'identificatore ViaggiaTreno ("S01030"), quello con cui si
	// chiedono i ritardi misurati sui treni di quella stazione.
	Codice string
}

// scaricaViaggiaTreno prende dall'elenco stazioni le due cose che servono: il
// nome breve, per riconoscere le fermate, e il codice, per i ritardi.
func scaricaViaggiaTreno() (map[string]datiVT, error) {
	out := map[string]datiVT{}
	var ultimoErr error
	for reg := 0; reg <= 22; reg++ {
		body, err := prendi(urlVT + strconv.Itoa(reg))
		if err != nil {
			ultimoErr = err
			continue
		}
		var elenco []stazioneVT
		if err := json.Unmarshal(body, &elenco); err != nil {
			continue
		}
		for _, s := range elenco {
			lungo := strings.TrimSpace(s.Localita.NomeLungo)
			breve := strings.TrimSpace(s.Localita.NomeBreve)
			if lungo == "" || breve == "" {
				continue
			}
			if c := stations.Canon(lungo); c != "" {
				out[c] = datiVT{Breve: breve, Codice: strings.TrimSpace(s.CodiceStazione)}
			}
		}
	}
	if len(out) == 0 && ultimoErr != nil {
		return nil, ultimoErr
	}
	return out, nil
}

func unisci(rfi map[int]string, vt map[string]datiVT) []*stations.Station {
	// I nomi canonici di ViaggiaTreno in ordine, per la seconda passata: la
	// prima è una lookup esatta, la seconda tollera le abbreviazioni.
	canoniVT := make([]string, 0, len(vt))
	for c := range vt {
		canoniVT = append(canoniVT, c)
	}
	sort.Strings(canoniVT)

	elenco := make([]*stations.Station, 0, len(rfi))
	esatti, fuzzy := 0, 0
	for id, nome := range rfi {
		s := &stations.Station{ID: id, Name: nome}
		c := stations.Canon(nome)
		if d, ok := vt[c]; ok {
			s.Aliases = append(s.Aliases, d.Breve)
			s.VT = d.Codice
			esatti++
		} else {
			// Un solo candidato o nessuno: due candidati vorrebbero dire che
			// l'abbreviazione è ambigua, e un alias ambiguo farebbe comparire
			// treni che non fermano dove dici tu.
			var trovato datiVT
			n := 0
			for _, cv := range canoniVT {
				if stations.Combacia(c, cv) {
					trovato, n = vt[cv], n+1
					if n > 1 {
						break
					}
				}
			}
			if n == 1 {
				s.Aliases = append(s.Aliases, trovato.Breve)
				s.VT = trovato.Codice
				fuzzy++
			}
		}
		s.Aliases = append(s.Aliases, aliasManuali[id]...)
		s.Aliases = ripulisci(s.Name, s.Aliases)
		elenco = append(elenco, s)
	}
	log.Printf("alias: %d per nome esatto, %d per abbreviazione", esatti, fuzzy)
	sort.Slice(elenco, func(i, j int) bool { return elenco[i].Name < elenco[j].Name })
	return elenco
}

// ripulisci toglie gli alias che non aggiungono niente: quelli vuoti, i
// doppioni, e quelli che in forma canonica coincidono già col nome ufficiale.
func ripulisci(nome string, alias []string) []string {
	base := stations.Canon(nome)
	visti := map[string]bool{base: true}
	out := alias[:0]
	for _, a := range alias {
		c := stations.Canon(a)
		if c == "" || visti[c] {
			continue
		}
		visti[c] = true
		out = append(out, a)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func prendi(url string) ([]byte, error) {
	cli := &http.Client{Timeout: 30 * time.Second}
	resp, err := cli.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func prendiHTML(url string) (*html.Node, error) {
	body, err := prendi(url)
	if err != nil {
		return nil, err
	}
	return html.Parse(strings.NewReader(string(body)))
}

func attr(n *html.Node, nome string) string {
	for _, a := range n.Attr {
		if a.Key == nome {
			return a.Val
		}
	}
	return ""
}

func testo(n *html.Node) string {
	var b strings.Builder
	var visita func(*html.Node)
	visita = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visita(c)
		}
	}
	visita(n)
	return b.String()
}
