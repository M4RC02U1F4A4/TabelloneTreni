package rfi

import (
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/dom"
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
			case n.Data == "h1" && dom.Attr(n, "id") == "nomeStazioneId":
				b.Station = pulisci(dom.Testo(n))
			case n.Data == "div" && dom.HaClasse(n, "marqueeinfosupp"):
				// Gli avvisi di stazione: un <div> figlio per avviso, dentro
				// il contenitore che la pagina fa scorrere in fondo. Sono
				// l'unico posto dove RFI dice degli ascensori guasti o dei
				// lavori sulla linea, e non hanno niente in comune col resto
				// del markup — nessun id, nessuna tabella, solo il nome della
				// classe con cui il CSS li anima.
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Type != html.ElementNode || c.Data != "div" {
						continue
					}
					if t := pulisci(dom.Testo(c)); t != "" {
						b.Notices = append(b.Notices, t)
					}
				}
				return // gli avvisi non si annidano
			case n.Data == "tr" && dom.Attr(n, "name") == "treno":
				// Una riga senza né orario né destinazione non è mostrabile:
				// diventerebbe una scheda vuota in mezzo all'elenco. Non se ne
				// sono viste finora, ma il costo di escluderle è nullo e il
				// costo di lasciarle passare lo paga chi guarda il tabellone.
				if t := leggiRiga(n); t.Time != "" || t.Terminus != "" {
					b.Trains = append(b.Trains, t)
				}
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
	t := Train{Number: strings.TrimSpace(dom.Attr(tr, "id"))}

	for td := tr.FirstChild; td != nil; td = td.NextSibling {
		if td.Type != html.ElementNode || td.Data != "td" {
			continue
		}
		switch dom.Attr(td, "id") {
		case idVettore:
			t.Carrier = pulisci(altImmagine(td))
		case idCategoria:
			// L'alt è nella forma "Categoria RE": il nome della categoria
			// esiste solo lì, perché il logo è una GIF inline.
			t.Category = pulisci(strings.TrimPrefix(pulisci(altImmagine(td)), "Categoria "))
		case idTreno:
			if n := pulisci(dom.Testo(td)); n != "" {
				t.Number = n
			}
		case idStazione:
			t.Terminus = pulisci(dom.Testo(td))
		case idOrario:
			t.Time = pulisci(dom.Testo(td))
		case idRitardo:
			switch v := pulisci(dom.Testo(td)); {
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
			t.Platform = pulisci(dom.Testo(td))
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
		if n.Type == html.ElementNode && n.Data == "div" && dom.HaClasse(n, "testoinfoaggiuntive") {
			txt := pulisci(dom.Testo(n))
			if i := strings.Index(txt, prefissoFerm); i >= 0 {
				stops = append(stops, leggiFermate(txt[i+len(prefissoFerm):])...)
			} else if txt != "" && !notaRidondante(txt) {
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

// notaRidondante dice se una nota di servizio non aggiunge niente a quello che
// la cella del ritardo dice già.
//
// Su un treno soppresso RFI nel popup ci scrive "SOPPRESSO -", col trattino di
// un motivo che non c'è: sullo schermo diventava un secondo "soppresso", in un
// altro colore e sotto quello che l'app mostra già leggendo la cella del
// ritardo. Se dopo la parola invece c'è qualcosa — "SOPPRESSO - PER SCIOPERO" —
// quello è il motivo, ed è la cosa che si voleva sapere: la nota resta.
//
// Il confronto si fa sulle sole lettere, perché la punteggiatura di quel
// trattino orfano non è garantita: si è visto un "-" secco, ma potrebbe essere
// un "–" o dei due punti.
func notaRidondante(s string) bool {
	lettere := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
	switch lettere {
	case "soppresso", "soppressa", "soppressi", "soppresse",
		"cancellato", "cancellata", "cancellati", "cancellate":
		return true
	}
	return false
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

func altImmagine(n *html.Node) string {
	if img := trovaImmagine(n); img != nil {
		return dom.Attr(img, "alt")
	}
	return ""
}

func trovaImmagine(n *html.Node) *html.Node {
	return dom.Trova(n, func(n *html.Node) bool { return n.Data == "img" })
}
