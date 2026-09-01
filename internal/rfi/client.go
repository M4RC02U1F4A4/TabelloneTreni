package rfi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const baseURL = "https://iechub.rfi.it/ArriviPartenze/ArrivalsDepartures/Monitor"

// UserAgent identifica l'applicazione verso RFI. Il sito non lo richiede — la
// pagina risponde anche senza — ma dichiararsi è il minimo per un client che
// interroga un servizio altrui a intervalli regolari.
var UserAgent = "TabelloneTreni (+https://github.com/M4RC02U1F4A4/TabelloneTreni)"

type Client struct {
	HTTP *http.Client
}

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// Fetch scarica e interpreta un tabellone. La pagina pesa circa 280 KB e RFI
// non la comprime nemmeno se gliela si chiede compressa: è la ragione per cui
// questa richiesta la fa il server una volta sola e non ogni client.
func (c *Client) Fetch(ctx context.Context, placeID int, arrivals bool) (*Board, error) {
	q := url.Values{
		"Arrivals": {strconv.FormatBool(arrivals)},
		"PlaceId":  {strconv.Itoa(placeID)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "text/html")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("richiesta a RFI: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("RFI ha risposto %s", resp.Status)
	}
	return Parse(resp.Body, placeID, arrivals)
}
