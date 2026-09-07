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

// Dettaglio è quello che Trenord dice di una linea sulla sua pagina.
//
// Porta il proprio orario di aggiornamento, ed è l'unica cosa in tutta questa
// fonte che permetta di riconoscere una risposta vecchia. Serve: lo stesso
// indirizzo, interrogato due volte di fila, risponde da backend diversi che non
// concordano — misurato, dieci richieste consecutive danno due risposte a caso,
// e le due varianti differiscono su una dozzina di linee. Senza il timestamp
// non c'è modo di sapere quale delle due sia quella di adesso.
type Dettaglio struct {
	Stato Stato `json:"status"`
	// Aggiornato non ha fuso dichiarato: si legge come UTC e si confronta solo
	// con altre letture dello stesso campo, che è tutto quello che serve.
	Aggiornato time.Time `json:"updated"`
	Avvisi     []Avviso  `json:"notices"`
}

// Dettaglio scarica la pagina di una linea.
//
// Costa una richiesta da oltre 130 KB, quasi tutta elenco delle stazioni: si
// chiede solo per le linee che qualcuno segue o che qualcuno sta guardando.
func (c *Client) Dettaglio(ctx context.Context, codice string) (*Dettaglio, error) {
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
	return ParseDettaglio(resp.Body)
}

// ParseDettaglio legge la pagina di una linea.
//
// Stessa forma dell'elenco: un JSON che incarta un frammento HTML. Gli avvisi
// stanno nel carosello, un blocco per avviso, ciascuno con la propria data e il
// proprio testo.
func ParseDettaglio(r io.Reader) (*Dettaglio, error) {
	var risp risposta
	if err := json.NewDecoder(r).Decode(&risp); err != nil {
		return nil, err
	}
	doc, err := html.Parse(strings.NewReader(risp.Message))
	if err != nil {
		return nil, err
	}
	d := &Dettaglio{}

	// Lo stato e l'orario stanno nell'intestazione, non nel carosello: il
	// carosello porta lo stato di ogni singola comunicazione, che è un'altra
	// cosa e vale il giorno in cui è stata scritta.
	if t := trova(doc, func(n *html.Node) bool { return haClasse(n, "title-icon") }); t != nil {
		if s, ok := statoDi(t); ok {
			d.Stato = s
		}
		if u := trova(t, func(n *html.Node) bool { return haClasse(n, "update-line") }); u != nil {
			// "Ultimo aggiornamento 07/09/26 16:50"
			campi := strings.Fields(pulisci(testo(u)))
			if len(campi) >= 2 {
				d.Aggiornato, _ = time.Parse("02/01/06 15:04",
					campi[len(campi)-2]+" "+campi[len(campi)-1])
			}
		}
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
	d.Avvisi = avvisi
	return d, nil
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
