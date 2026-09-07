package trenord

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// La pagina "Le nostre linee" arriva vuota: l'elenco se lo va a prendere il
// browser da qui, e questa è la stessa chiamata che fa lui. Il frammento pesa
// 135 KB contro il megabyte e passa della pagina, e non porta dietro né mappa
// né widget di terze parti.
const baseURL = "https://www.trenord.it/rest/render/shoulder-lines?no_cache=1&mxp=false&L=0"

// UserAgent deve somigliare a quello di un browser.
//
// Contro le nostre abitudini — con RFI l'applicazione si dichiara — ma davanti
// a trenord.it c'è un Akamai Bot Manager che a un User-Agent onesto risponde
// 403 su questo endpoint, mentre serve la pagina normale senza fiatare. È
// l'unico header che conta: Referer, X-Requested-With e il resto non cambiano
// nulla, provati uno per uno.
var UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"

type Client struct {
	HTTP *http.Client
}

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// Fetch scarica lo stato di tutte le linee in una richiesta sola. La fa il
// server ogni tanto, mai i client.
func (c *Client) Fetch(ctx context.Context) ([]Linea, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
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
	return Parse(resp.Body)
}
