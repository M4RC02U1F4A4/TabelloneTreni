// Package vt legge i ritardi che ViaggiaTreno misura sui treni.
//
// Serve perché il tabellone di RFI, che è la fonte principale di questa app,
// pubblica il ritardo con parsimonia, e in due modi diversi. Sotto i pochi
// minuti arrotonda a zero: su un campione di venti treni, cinque viaggiavano
// fra +1 e +2 con il tabellone che dichiarava zero. Sopra l'ora si aggiorna con
// calma: un treno che ne aveva 95 sul tabellone ne dichiarava 70.
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

// Treno è quello che ViaggiaTreno sa di un treno del tabellone.
type Treno struct {
	// Ritardo in minuti, negativo se il treno è in anticipo. È nil finché il
	// treno non è stato rilevato da nessuna parte: lì una misura non esiste, e
	// lo zero che ViaggiaTreno manda è il default del campo, non un dato.
	Ritardo *int
	// I due binari, come li dichiara ViaggiaTreno: il previsto e quello
	// assegnato davvero. Uno dei due può mancare, e allora non si può dire
	// niente su un eventuale cambio.
	BinarioProgrammato string
	BinarioEffettivo   string
	// Le coordinate con cui si chiede l'andamento di questo treno. Non servono
	// al tabellone, servono a chi poi apre la scheda del singolo treno.
	CodOrigine   string
	DataPartenza int64
}

// Cambiato dice se il treno parte da un binario diverso da quello previsto.
//
// Serve che ci siano tutti e due: con un solo valore non si sta confrontando
// niente, e un "cambiato" annunciato per un dato mancante manderebbe la gente a
// cercare un binario che non è cambiato affatto.
func (t Treno) Cambiato() bool {
	return t.BinarioProgrammato != "" && t.BinarioEffettivo != "" &&
		t.BinarioProgrammato != t.BinarioEffettivo
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
// Si leggono otto campi su una trentina: il resto non serve a niente qui.
type treno struct {
	NumeroTreno       int      `json:"numeroTreno"`
	Ritardo           float64  `json:"ritardo"`
	CompRitardo       []string `json:"compRitardo"`
	CodOrigine        string   `json:"codOrigine"`
	DataPartenzaTreno int64    `json:"dataPartenzaTreno"`

	BinarioProgrammatoPartenza string `json:"binarioProgrammatoPartenzaDescrizione"`
	BinarioEffettivoPartenza   string `json:"binarioEffettivoPartenzaDescrizione"`
	BinarioProgrammatoArrivo   string `json:"binarioProgrammatoArrivoDescrizione"`
	BinarioEffettivoArrivo     string `json:"binarioEffettivoArrivoDescrizione"`
}

// Treni restituisce quello che ViaggiaTreno sa dei treni di una stazione,
// indicizzato per numero di treno.
//
// codice è quello di ViaggiaTreno ("S01645"), non il PlaceId di RFI.
//
// Ci sono dentro tutti i treni della risposta, non solo quelli già rilevati: il
// ritardo di un treno non ancora partito non esiste, ma il suo binario sì — ed
// è proprio prima della partenza che un cambio di binario conta.
func (c *Client) Treni(ctx context.Context, codice string, arrivi bool) (map[string]Treno, error) {
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
		return map[string]Treno{}, nil
	}

	var elenco []treno
	if err := json.Unmarshal(body, &elenco); err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}

	out := make(map[string]Treno, len(elenco))
	for _, t := range elenco {
		v := Treno{CodOrigine: t.CodOrigine, DataPartenza: t.DataPartenzaTreno}
		if rilevato(t) {
			minuti := int(math.Round(t.Ritardo))
			v.Ritardo = &minuti
		}
		// Su un tabellone arrivi il binario che interessa è quello di arrivo:
		// i campi di partenza lì restano vuoti, e viceversa.
		if arrivi {
			v.BinarioProgrammato, v.BinarioEffettivo = t.BinarioProgrammatoArrivo, t.BinarioEffettivoArrivo
		} else {
			v.BinarioProgrammato, v.BinarioEffettivo = t.BinarioProgrammatoPartenza, t.BinarioEffettivoPartenza
		}
		out[strconv.Itoa(t.NumeroTreno)] = v
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
