package rfi

import (
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// Le celle della tabella si riconoscono dall'id, che però RFI ripete identico
// su ogni riga: vanno cercati dentro la <tr>, mai con una lookup globale.
const (
	idVettore    = "RVettore"
	idCategoria  = "RCategoria"
	idTreno      = "RTreno"
	idStazione   = "RStazione"
	idOrario     = "ROrario"
	idRitardo    = "RRitardo"
	idBinario    = "RBinario"
	idLampeggio  = "RExLampeggio"
	idDettagli   = "RDettagli"
	prefissoFerm = "FERMA A:"
)

var reFermata = regexp.MustCompile(`([^()]+?)\s*\((\d{1,2}:\d{2})\)`)

// L'etichetta di aggiornamento è una frase ("MONITOR ARRIVI & PARTENZE LIVE
// aggiornato il 01/09/2026 alle ore 22:07:56"): al client serve solo l'istante.
var reAggiornamento = regexp.MustCompile(`(\d{2}/\d{2}/\d{4}).*?(\d{2}:\d{2}:\d{2})`)

// Parse interpreta la pagina del monitor.
func Parse(r io.Reader, placeID int, arrivals bool) (*Board, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	b := &Board{PlaceID: placeID, Arrivals: arrivals, Trains: []Train{}}

	var visita func(*html.Node)
	visita = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch {
			case n.Data == "h1" && attr(n, "id") == "nomeStazioneId":
				b.Station = pulisci(testo(n))
			case n.Data == "label" && attr(n, "id") == "UltimoaggiData":
				if m := reAggiornamento.FindStringSubmatch(pulisci(testo(n))); m != nil {
					b.Updated = m[1] + " " + m[2]
				}
			case n.Data == "tr" && attr(n, "name") == "treno":
				b.Trains = append(b.Trains, leggiRiga(n))
				return // le righe non si annidano
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visita(c)
		}
	}
	visita(doc)

	if b.Station == "" && len(b.Trains) == 0 {
		return nil, fmt.Errorf("pagina senza stazione né treni: PlaceId %d inesistente o markup cambiato", placeID)
	}
	return b, nil
}

func leggiRiga(tr *html.Node) Train {
	t := Train{Number: strings.TrimSpace(attr(tr, "id"))}

	for td := tr.FirstChild; td != nil; td = td.NextSibling {
		if td.Type != html.ElementNode || td.Data != "td" {
			continue
		}
		switch attr(td, "id") {
		case idVettore:
			t.Carrier = pulisci(altImmagine(td))
		case idCategoria:
			// L'alt è nella forma "Categoria RE": il nome della categoria
			// esiste solo lì, perché il logo è una GIF inline.
			t.Category = pulisci(strings.TrimPrefix(pulisci(altImmagine(td)), "Categoria "))
		case idTreno:
			if n := pulisci(testo(td)); n != "" {
				t.Number = n
			}
		case idStazione:
			t.Terminus = pulisci(testo(td))
		case idOrario:
			t.Time = pulisci(testo(td))
		case idRitardo:
			switch v := pulisci(testo(td)); {
			case v == "":
			case strings.EqualFold(v, "Cancellato"):
				t.Cancelled = true
			default:
				if n, err := strconv.Atoi(v); err == nil {
					t.Delay = n
				} else {
					t.Status = v
				}
			}
		case idBinario:
			t.Platform = pulisci(testo(td))
		case idLampeggio:
			// Il lampeggio è la presenza dell'immagine, non un testo: l'unico
			// altro indizio è un aria-label che RFI emette con le virgolette
			// sfuggite all'escaping, quindi inaffidabile.
			t.Boarding = trovaImmagine(td) != nil
		case idDettagli:
			t.Stops, t.Notes = leggiDettagli(td)
		}
	}
	return t
}

// leggiDettagli estrae fermate e note dal popup nascosto nella cella. Il popup
// può contenere più blocchi (le fermate e un testo libero di servizio), quindi
// si distinguono dal contenuto e non dalla posizione.
func leggiDettagli(td *html.Node) ([]Stop, string) {
	var stops []Stop
	var note []string

	var visita func(*html.Node)
	visita = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "div" && haClasse(n, "testoinfoaggiuntive") {
			txt := pulisci(testo(n))
			if i := strings.Index(txt, prefissoFerm); i >= 0 {
				stops = append(stops, leggiFermate(txt[i+len(prefissoFerm):])...)
			} else if txt != "" {
				note = append(note, txt)
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visita(c)
		}
	}
	visita(td)
	return stops, strings.Join(note, " · ")
}

// leggiFermate scompone "MI BOVISA P. (21:41) - SARONNO (21:54) - ...".
//
// Il taglio è fatto con una regexp sulle parentesi invece che sul separatore
// " - " perché il trattino compare anche dentro i nomi ("LISSONE-MUGGIO",
// "S. ZENONE AL L.").
func leggiFermate(s string) []Stop {
	m := reFermata.FindAllStringSubmatch(s, -1)
	out := make([]Stop, 0, len(m))
	for _, f := range m {
		nome := strings.Trim(pulisci(f[1]), "-–— ")
		if nome == "" {
			continue
		}
		out = append(out, Stop{Name: nome, Time: f[2]})
	}
	return out
}

// pulisci compatta gli spazi e finisce di sciogliere le entity HTML. RFI
// applica l'escaping più di una volta agli apostrofi, per cui dopo il parser
// resta ancora del "&#39;" letterale nel testo ("CANTU&#39;&#39;-CERMENATE").
func pulisci(s string) string {
	for i := 0; i < 3 && strings.Contains(s, "&"); i++ {
		d := html.UnescapeString(s)
		if d == s {
			break
		}
		s = d
	}
	return strings.Join(strings.Fields(s), " ")
}

func attr(n *html.Node, nome string) string {
	for _, a := range n.Attr {
		if a.Key == nome {
			return a.Val
		}
	}
	return ""
}

func haClasse(n *html.Node, c string) bool {
	for _, f := range strings.Fields(attr(n, "class")) {
		if f == c {
			return true
		}
	}
	return false
}

func trovaImmagine(n *html.Node) *html.Node {
	if n.Type == html.ElementNode && n.Data == "img" {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if r := trovaImmagine(c); r != nil {
			return r
		}
	}
	return nil
}

func altImmagine(n *html.Node) string {
	if img := trovaImmagine(n); img != nil {
		return attr(img, "alt")
	}
	return ""
}

func testo(n *html.Node) string {
	var b strings.Builder
	var visita func(*html.Node)
	visita = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visita(c)
		}
	}
	visita(n)
	return b.String()
}
