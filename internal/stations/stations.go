// Package stations tiene il catalogo delle stazioni e sa riconoscere una
// stazione dal nome con cui compare nell'elenco delle fermate di un treno.
package stations

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
)

//go:embed stations.json
var catalogoJSON []byte

// Station è una stazione presente nel tabellone RFI. Gli alias sono le altre
// grafie con cui la stessa stazione può comparire — soprattutto il nome breve
// di ViaggiaTreno, che è la forma abbreviata che RFI stampa nelle fermate.
type Station struct {
	ID      int      `json:"i"`
	Name    string   `json:"n"`
	Aliases []string `json:"a,omitempty"`

	forme []string // Name e Aliases in forma canonica, pronti al confronto
}

type file struct {
	Generated string     `json:"generated"`
	Stations  []*Station `json:"stations"`
}

// Catalogo è l'elenco completo, indicizzato per PlaceId.
type Catalogo struct {
	Generated string
	Elenco    []*Station
	perID     map[int]*Station
}

var Default *Catalogo

func init() {
	c, err := Load(catalogoJSON)
	if err != nil {
		panic("catalogo stazioni non caricabile: " + err.Error())
	}
	Default = c
}

func Load(raw []byte) (*Catalogo, error) {
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	if len(f.Stations) == 0 {
		return nil, fmt.Errorf("catalogo vuoto")
	}
	c := &Catalogo{Generated: f.Generated, Elenco: f.Stations, perID: make(map[int]*Station, len(f.Stations))}
	for _, s := range f.Stations {
		s.forme = append(s.forme, Canon(s.Name))
		for _, a := range s.Aliases {
			if ca := Canon(a); ca != "" && ca != s.forme[0] {
				s.forme = append(s.forme, ca)
			}
		}
		c.perID[s.ID] = s
	}
	sort.Slice(c.Elenco, func(i, j int) bool { return c.Elenco[i].Name < c.Elenco[j].Name })
	return c, nil
}

func (c *Catalogo) ByID(id int) *Station { return c.perID[id] }

// Matcher riconosce una stazione fra i nomi delle fermate di un treno.
//
// Confronta con tutte le forme note della stazione perché nessuna singola basta:
// il tabellone scrive "MI BOVISA P." dove il catalogo ha "MILANO BOVISA
// POLITECNICO", e la sola somiglianza di stringa non le unirebbe.
type Matcher struct{ forme []string }

func (c *Catalogo) Matcher(id int) *Matcher {
	s := c.perID[id]
	if s == nil {
		return nil
	}
	return &Matcher{forme: s.forme}
}

func (m *Matcher) Matches(nome string) bool {
	if m == nil {
		return false
	}
	c := Canon(nome)
	if c == "" {
		return false
	}
	for _, f := range m.forme {
		if Combacia(c, f) {
			return true
		}
	}
	return false
}
