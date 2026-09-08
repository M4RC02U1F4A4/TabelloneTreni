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
	// VT è il codice della stessa stazione su ViaggiaTreno ("S01030"), da cui
	// si leggono i ritardi misurati sui treni. È vuoto per le stazioni che
	// ViaggiaTreno non ha o che non si è riusciti ad accoppiare: lì il
	// tabellone resta quello di RFI e basta.
	VT string `json:"v,omitempty"`
	// VTAlt sono gli altri codici ViaggiaTreno che servono lo *stesso*
	// tabellone RFI. Non stanno nel catalogo su disco: si ricavano al
	// caricamento, vedi collegaSotterranee.
	VTAlt []string `json:"-"`
	// Dove sta la stazione, per poterla disegnare su una mappa. Le raccoglie
	// genstations da ViaggiaTreno, che è la sola delle due fonti a pubblicarle;
	// sono zero per le stazioni che non si è riusciti a interrogare, e zero
	// vuol dire "non lo sappiamo" — non il largo del golfo di Guinea, che è
	// dove cadrebbe un puntino disegnato a quelle coordinate.
	Lat float64 `json:"la,omitempty"`
	Lon float64 `json:"lo,omitempty"`

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
	// perVT ritrova una stazione dal codice ViaggiaTreno. Serve alle fermate di
	// un viaggio, che ViaggiaTreno identifica solo con quel codice: è l'unico
	// modo di attaccargli le coordinate.
	perVT map[string]*Station
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
	c := &Catalogo{
		Generated: f.Generated, Elenco: f.Stations,
		perID: make(map[int]*Station, len(f.Stations)),
		perVT: make(map[string]*Station, len(f.Stations)),
	}
	for _, s := range f.Stations {
		s.forme = append(s.forme, Canon(s.Name))
		for _, a := range s.Aliases {
			if ca := Canon(a); ca != "" && ca != s.forme[0] {
				s.forme = append(s.forme, ca)
			}
		}
		c.perID[s.ID] = s
		// Chi vince quando due tabelloni RFI condividono un codice — succede
		// con superficie e sotterranea — è chi ha le coordinate: le stesse per
		// entrambi a meno di poche decine di metri, ma una delle due potrebbe
		// non averle affatto.
		if s.VT != "" {
			if gia := c.perVT[s.VT]; gia == nil || (gia.Lat == 0 && s.Lat != 0) {
				c.perVT[s.VT] = s
			}
		}
	}
	sort.Slice(c.Elenco, func(i, j int) bool { return c.Elenco[i].Name < c.Elenco[j].Name })
	c.collegaSotterranee()
	return c, nil
}

// suffissoSotterranea è come RFI chiama il piano inferiore di una stazione che
// ne ha due.
const suffissoSotterranea = " SOTTERRANEA"

// collegaSotterranee unisce i due livelli di una stazione che ne ha due.
//
// Serve perché le due fonti li trattano in modo opposto. Il tabellone RFI della
// stazione "di sopra" è già la somma dei due: a Milano Porta Garibaldi porta
// quaranta treni, diciassette dei quali partono da un binario "SOT".
// ViaggiaTreno invece tiene due stazioni separate, e alla stazione di
// superficie risponde con i soli treni di superficie.
//
// Senza questo collegamento tutti i treni del piano inferiore — cioè le linee
// suburbane, cioè quelle che prende più gente — restano senza il ritardo
// misurato e senza il binario cambiato, e il difetto si vede solo nelle ore in
// cui quei treni ci sono.
//
// Il criterio è il nome, non una lista scritta a mano: sono due casi oggi
// (Milano Porta Garibaldi e Genova Piazza Principe) ma il giorno che RFI ne
// aggiunge un terzo funziona da solo. Un confronto più largo — "un nome che
// comincia per quest'altro" — non andrebbe bene: catturerebbe ALBA e ALBA
// ADRIATICA, che sono due paesi diversi.
func (c *Catalogo) collegaSotterranee() {
	perNome := make(map[string]*Station, len(c.Elenco))
	for _, s := range c.Elenco {
		perNome[Canon(s.Name)] = s
	}
	for _, s := range c.Elenco {
		giu := perNome[Canon(s.Name+suffissoSotterranea)]
		if giu == nil || giu.VT == "" || giu.VT == s.VT {
			continue
		}
		s.VTAlt = append(s.VTAlt, giu.VT)
	}
}

// CodiciVT sono tutti i codici ViaggiaTreno da interrogare per avere i treni
// che questo tabellone mostra: il suo, più gli eventuali altri livelli.
func (s *Station) CodiciVT() []string {
	if s.VT == "" {
		return nil
	}
	return append([]string{s.VT}, s.VTAlt...)
}

func (c *Catalogo) ByID(id int) *Station { return c.perID[id] }

// ByVT ritrova una stazione dal codice ViaggiaTreno. nil se quel codice non è
// accoppiato a nessun tabellone RFI, che per una fermata di passaggio capita.
func (c *Catalogo) ByVT(codice string) *Station { return c.perVT[codice] }

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
