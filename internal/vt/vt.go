// Package vt legge i ritardi che ViaggiaTreno misura sui treni.
//
// Serve perché il tabellone di RFI, che è la fonte principale di questa app,
// pubblica il ritardo con parsimonia: campionando Gallarate alle 21 di sera,
// RFI dava zero su tutti e trentacinque i treni mentre ViaggiaTreno, negli
// stessi minuti, ne dava sette in ritardo fra uno e quattro minuti. Il ritardo
// piccolo — quello che decide se il treno si prende o no — su RFI non c'è.
//
// Le due letture restano separate fino allo schermo: qui non si sceglie quale
// sia quella giusta, si mostrano tutt'e due.
package vt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"
)

const base = "http://www.viaggiatreno.it/infomobilita/resteasy/viaggiatreno"

// Ritardo è la misura su un singolo treno.
type Ritardo struct {
	// Minuti può essere negativo: un treno in anticipo esiste.
	Minuti int
}

// Client interroga ViaggiaTreno. Lo zero value non è utilizzabile: usare New.
type Client struct {
	hc   *http.Client
	base string
}

func NewClient() *Client {
	return &Client{
		// Il servizio è lento e ogni tanto non risponde affatto. Il timeout sta
		// sotto a quello del tabellone RFI perché questa è la lettura
		// facoltativa delle due: se tarda, si serve il tabellone senza.
		hc:   &http.Client{Timeout: 8 * time.Second},
		base: base,
	}
}

// treno è la forma del singolo elemento nelle risposte partenze/arrivi.
// Si leggono tre campi su una trentina: il resto non serve a niente qui.
type treno struct {
	NumeroTreno int      `json:"numeroTreno"`
	Ritardo     float64  `json:"ritardo"`
	CompRitardo []string `json:"compRitardo"`
}

// Ritardi restituisce i ritardi misurati, indicizzati per numero di treno.
//
// codice è quello di ViaggiaTreno ("S01030"), non il PlaceId di RFI.
func (c *Client) Ritardi(ctx context.Context, codice string, arrivi bool) (map[string]Ritardo, error) {
	verso := "partenze"
	if arrivi {
		verso = "arrivi"
	}
	// ViaggiaTreno vuole la data nel formato di Date.prototype.toString() di
	// JavaScript, perché il suo frontend gliela passa così.
	quando := time.Now().Format("Mon Jan 02 2006 15:04:05 GMT-0700")
	url := fmt.Sprintf("%s/%s/%s/%s", c.base, verso, codice, urlQuote(quando))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	// Una stazione senza partenze risponde 200 con corpo vuoto, non con "[]".
	if len(body) == 0 {
		return map[string]Ritardo{}, nil
	}

	var elenco []treno
	if err := json.Unmarshal(body, &elenco); err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}

	out := make(map[string]Ritardo, len(elenco))
	for _, t := range elenco {
		if !rilevato(t) {
			continue
		}
		out[strconv.Itoa(t.NumeroTreno)] = Ritardo{Minuti: int(math.Round(t.Ritardo))}
	}
	return out, nil
}

// rilevato dice se quel ritardo è una misura o soltanto il valore di partenza
// del campo.
//
// Un treno che non è ancora stato visto da nessun punto di controllo arriva con
// `ritardo: 0` esattamente come un treno puntuale: lo zero è il default dello
// schema. A separarli è compRitardo, che sul primo dice "non partito" e sul
// secondo "in orario". Mostrare quello zero vorrebbe dire dare per misurato un
// numero che nessuno ha misurato.
//
// Un ritardo diverso da zero è invece per forza una misura, e vale anche se
// compRitardo dicesse qualcosa che qui non si conosce: l'insieme dei valori non
// è documentato da nessuna parte, e la regola non deve rompersi il giorno che
// ne compare uno nuovo.
func rilevato(t treno) bool {
	if t.Ritardo != 0 {
		return true
	}
	return len(t.CompRitardo) > 0 && t.CompRitardo[0] != "non partito"
}

// urlQuote codifica gli spazi e i due punti della data. Non si usa
// url.PathEscape perché lascerebbe i due punti come sono e ViaggiaTreno, su
// quelli, risponde 404.
func urlQuote(s string) string {
	const esadecimale = "0123456789ABCDEF"
	out := make([]byte, 0, len(s)*3)
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_' || b == '.' || b == '~' {
			out = append(out, b)
			continue
		}
		out = append(out, '%', esadecimale[b>>4], esadecimale[b&0x0f])
	}
	return string(out)
}
