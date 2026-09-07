package trenord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Avviso è una comunicazione della sala operativa su una linea: lavori,
// scioperi, guasti. È il testo che manca al bollino, che dice solo che
// qualcosa non va.
type Avviso struct {
	// Data è quando Trenord l'ha pubblicato, non quando l'abbiamo letto: serve
	// a riconoscere quelli nuovi senza tenere memoria di quelli vecchi.
	Data  time.Time `json:"date"`
	Testo string    `json:"text"`
}

const dettaglioURL = "https://www.trenord.it/rest/render/line-details"

// Avvisi scarica le comunicazioni di una linea.
//
// Costa una richiesta da oltre 130 KB per linea, quasi tutta elenco delle
// stazioni: si chiede solo per le linee che qualcuno segue davvero, che sono
// due o tre e non sessantacinque.
func (c *Client) Avvisi(ctx context.Context, codice string) ([]Avviso, error) {
	q := url.Values{"code": {strings.ToUpper(codice)}, "L": {"0"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dettaglioURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "it-IT,it;q=0.9")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("richiesta a Trenord: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Trenord ha risposto %s", resp.Status)
	}
	return ParseAvvisi(resp.Body)
}

// ParseAvvisi legge le comunicazioni dalla risposta del dettaglio linea.
//
// Stessa forma dell'elenco: un JSON che incarta un frammento HTML. Gli avvisi
// stanno nel carosello, un blocco per avviso, ciascuno con la propria data e il
// proprio testo.
func ParseAvvisi(r io.Reader) ([]Avviso, error) {
	var risp risposta
	if err := json.NewDecoder(r).Decode(&risp); err != nil {
		return nil, err
	}
	doc, err := html.Parse(strings.NewReader(risp.Message))
	if err != nil {
		return nil, err
	}

	// Una pagina senza carosello non è una linea senza avvisi: è una risposta
	// che non abbiamo capito, e le due cose non vanno confuse — la prima è
	// normale, la seconda va vista.
	carosello := trova(doc, func(n *html.Node) bool {
		return n.Data == "div" && haClasse(n, "carousel-line")
	})
	if carosello == nil {
		return nil, errors.New("nessun carosello nel dettaglio linea: markup cambiato")
	}

	var avvisi []Avviso
	for c := carosello.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || !haClasse(c, "item") {
			continue
		}
		corpo := pulisci(testo(trova(c, func(n *html.Node) bool {
			return haClasse(n, "body-texts")
		})))
		if corpo == "" {
			continue
		}
		a := Avviso{Testo: corpo}
		if d := trova(c, func(n *html.Node) bool { return haClasse(n, "news-date") }); d != nil {
			// Il formato è ISO con i millisecondi e la Z: se un giorno cambia,
			// meglio un avviso senza data che nessun avviso.
			a.Data, _ = time.Parse(time.RFC3339, pulisci(testo(d)))
		}
		avvisi = append(avvisi, a)
	}
	return avvisi, nil
}

// trova restituisce il primo nodo che soddisfa la condizione.
func trova(n *html.Node, ok func(*html.Node) bool) *html.Node {
	if n == nil {
		return nil
	}
	if n.Type == html.ElementNode && ok(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if r := trova(c, ok); r != nil {
			return r
		}
	}
	return nil
}
