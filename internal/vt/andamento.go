package vt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
)

// Andamento è dove si trova un treno adesso e come sta andando il suo viaggio.
//
// Il tabellone dice quanto è in ritardo; questo dice dov'è, che è la domanda
// che ci si fa davvero quando il numero è grosso e ci si chiede se il treno
// arriverà mai.
type Andamento struct {
	// Ritardo all'ultimo rilevamento, in minuti.
	Ritardo int
	// Dove e quando il treno è stato visto l'ultima volta. Stazione è vuota
	// finché il treno non passa da un punto di controllo: ViaggiaTreno lì
	// scrive "--", che non è il nome di nessun posto.
	Stazione string
	Ora      time.Time
	Fermate  []Fermata
}

// Fermata è una tappa del viaggio, con l'orario previsto e — se il treno ci è
// già passato — quello reale.
type Fermata struct {
	Codice      string
	Nome        string
	Programmata time.Time
	// Effettiva è valorizzata solo sulle fermate già servite.
	Effettiva time.Time
	// Ritardo ha senso solo insieme a Effettiva: sulle fermate ancora da fare
	// ViaggiaTreno lascia zero, che non è una previsione ma un campo non
	// compilato.
	Ritardo int
	Passata bool
}

type andamento struct {
	Ritardo                   float64   `json:"ritardo"`
	StazioneUltimoRilevamento string    `json:"stazioneUltimoRilevamento"`
	OraUltimoRilevamento      int64     `json:"oraUltimoRilevamento"`
	Fermate                   []fermata `json:"fermate"`
}

type fermata struct {
	ID          string  `json:"id"`
	Stazione    string  `json:"stazione"`
	Programmata int64   `json:"programmata"`
	Effettiva   int64   `json:"effettiva"`
	Ritardo     float64 `json:"ritardo"`
}

// Andamento legge il viaggio di un singolo treno.
//
// Le tre coordinate arrivano da Treni: ViaggiaTreno identifica un treno con il
// numero *più* la stazione di origine e il giorno di partenza, perché lo stesso
// numero torna ogni giorno e su relazioni diverse.
func (c *Client) Andamento(ctx context.Context, codOrigine, numero string, data int64) (*Andamento, error) {
	url := fmt.Sprintf("%s/andamentoTreno/%s/%s/%d", c.base, codOrigine, numero, data)

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
	// Un treno che ViaggiaTreno non traccia risponde 200 con il corpo vuoto.
	if len(body) == 0 {
		return nil, nil
	}

	var a andamento
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}

	out := &Andamento{
		Ritardo:  int(math.Round(a.Ritardo)),
		Stazione: nomeStazione(a.StazioneUltimoRilevamento),
		Ora:      quando(a.OraUltimoRilevamento),
		Fermate:  make([]Fermata, 0, len(a.Fermate)),
	}
	for _, f := range a.Fermate {
		out.Fermate = append(out.Fermate, Fermata{
			Codice:      f.ID,
			Nome:        f.Stazione,
			Programmata: quando(f.Programmata),
			Effettiva:   quando(f.Effettiva),
			Ritardo:     int(math.Round(f.Ritardo)),
			// La fermata è servita quando ha un orario reale. È il solo segnale
			// che non richiede di indovinare: il ritardo per fermata resta a
			// zero su tutte quelle ancora da fare.
			Passata: f.Effettiva != 0,
		})
	}
	return out, nil
}

// nomeStazione ripulisce il segnaposto che ViaggiaTreno usa quando il treno non
// è ancora stato visto da nessuna parte.
func nomeStazione(s string) string {
	if s == "--" {
		return ""
	}
	return s
}

func quando(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
