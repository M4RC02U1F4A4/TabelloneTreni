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
	"time"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/stations"
	"golang.org/x/net/html"
)

const (
	urlRFI = "https://iechub.rfi.it/ArriviPartenze"
	urlVT  = "http://www.viaggiatreno.it/infomobilita/resteasy/viaggiatreno/elencoStazioni/"
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
	flag.Parse()

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

	conAlias := 0
	for _, s := range elenco {
		if len(s.Aliases) > 0 {
			conAlias++
		}
	}
	fmt.Printf("scritte %d stazioni in %s (%d con alias, %d KB)\n",
		len(elenco), *out, conAlias, len(body)/1024)
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
	Localita struct {
		NomeLungo string `json:"nomeLungo"`
		NomeBreve string `json:"nomeBreve"`
	} `json:"localita"`
}

// scaricaViaggiaTreno serve solo per i nomi brevi: sono la stessa forma
// abbreviata che RFI stampa nelle fermate ("MI BOVISA P.", "MI.P.GARIBALDI"),
// e sono quindi il ponte fra le due grafie.
func scaricaViaggiaTreno() (map[string]string, error) {
	out := map[string]string{}
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
				out[c] = breve
			}
		}
	}
	if len(out) == 0 && ultimoErr != nil {
		return nil, ultimoErr
	}
	return out, nil
}

func unisci(rfi map[int]string, vt map[string]string) []*stations.Station {
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
		if breve, ok := vt[c]; ok {
			s.Aliases = append(s.Aliases, breve)
			esatti++
		} else {
			// Un solo candidato o nessuno: due candidati vorrebbero dire che
			// l'abbreviazione è ambigua, e un alias ambiguo farebbe comparire
			// treni che non fermano dove dici tu.
			var trovato string
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
				s.Aliases = append(s.Aliases, trovato)
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
