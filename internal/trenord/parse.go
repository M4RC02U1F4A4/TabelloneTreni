package trenord

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/dom"
	"golang.org/x/net/html"
)

// risposta è quello che manda l'endpoint: un oggetto JSON con dentro, come
// stringa, il frammento HTML che la pagina si innesta nell'elenco. Nome del
// campo compreso: "message" lo è anche quando va tutto bene.
type risposta struct {
	Message string `json:"message"`
}

// Parse interpreta la risposta dell'elenco linee.
func Parse(r io.Reader) ([]Linea, error) {
	var risp risposta
	if err := json.NewDecoder(r).Decode(&risp); err != nil {
		return nil, err
	}
	return parseHTML(strings.NewReader(risp.Message))
}

// parseHTML legge il frammento con l'elenco.
//
// Il frammento è generato da un template che emette moltissimi blocchi vuoti
// fra una linea e l'altra, e gruppi interi senza nemmeno una voce (li nasconde
// poi via JavaScript). Per questo si parte dai <a data-code>, che ci sono solo
// dove c'è davvero una linea, invece di scendere per posizione.
func parseHTML(r io.Reader) ([]Linea, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}

	var linee []Linea
	var visita func(n *html.Node, gruppo string)
	visita = func(n *html.Node, gruppo string) {
		if n.Type == html.ElementNode {
			switch {
			case n.Data == "ul" && dom.HaClasse(n, "new_line"):
				// Il titolo del gruppo è dentro la <ul>, non prima: da qui in
				// giù tutte le linee appartengono a questo gruppo.
				gruppo = titoloGruppo(n)
			case n.Data == "a" && dom.Attr(n, "data-code") != "":
				if l, ok := leggiLinea(n, gruppo); ok {
					linee = append(linee, l)
				}
				return // una linea non ne contiene un'altra
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visita(c, gruppo)
		}
	}
	visita(doc, "")

	if len(linee) == 0 {
		return nil, errors.New("nessuna linea nell'elenco: markup cambiato")
	}
	return linee, nil
}

func leggiLinea(a *html.Node, gruppo string) (Linea, bool) {
	l := Linea{
		Codice: strings.TrimSpace(dom.Attr(a, "data-code")),
		Nome:   pulisci(dom.Attr(a, "data-name")),
		Gruppo: gruppo,
	}
	// Il nome sta nell'attributo e anche nel testo della voce: l'attributo è
	// quello che Trenord usa per il proprio filtro di ricerca, il testo porta
	// spazi di impaginazione. Se l'attributo manca si ripiega sul testo.
	if l.Nome == "" {
		l.Nome = pulisci(dom.Testo(a))
	}
	stato, ok := statoDi(a)
	if !ok {
		// Una linea senza semaforo non è una linea a stato ignoto: è markup
		// che non riconosciamo più, e mostrarla verde sarebbe una bugia.
		return Linea{}, false
	}
	l.Stato = stato
	return l, l.Codice != "" && l.Nome != ""
}

// statoDi legge il semaforo dalle classi del primo div .status-line che ne
// porta uno riconoscibile.
func statoDi(n *html.Node) (Stato, bool) {
	var s Stato
	trovato := dom.Trova(n, func(n *html.Node) bool {
		if !dom.HaClasse(n, "status-line") {
			return false
		}
		switch {
		case dom.HaClasse(n, "danger"):
			s = Grave
		case dom.HaClasse(n, "critical"):
			s = Critico
		case dom.HaClasse(n, "green-line"):
			s = Regolare
		default:
			return false
		}
		return true
	})
	return s, trovato != nil
}

func titoloGruppo(ul *html.Node) string {
	for c := ul.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "p" && dom.HaClasse(c, "title-line") {
			return pulisci(dom.Testo(c))
		}
	}
	return ""
}

/*
Le comunicazioni di Trenord le scrive una persona, spesso incollando da

	Word, e arrivano con i byte 0x80-0x9F lasciati passare cosi' com'erano: in
	Windows-1252 sono virgolette e trattini tipografici, ma decodificati come
	Latin-1 diventano caratteri di controllo, che sul telefono si vedono come un
	quadratino in mezzo a una parola.

	La tabella e' quella di CP1252 per quell'intervallo. Le cinque posizioni che
	li' non sono assegnate restano a zero e vengono buttate: meglio un carattere
	in meno che un quadratino.
*/
var cp1252 = [32]rune{
	'\u20ac', 0, '\u201a', '\u0192', '\u201e', '\u2026', '\u2020', '\u2021',
	'\u02c6', '\u2030', '\u0160', '\u2039', '\u0152', 0, '\u017d', 0,
	0, '\u2018', '\u2019', '\u201c', '\u201d', '\u2022', '\u2013', '\u2014',
	'\u02dc', '\u2122', '\u0161', '\u203a', '\u0153', 0, '\u017e', '\u0178',
}

// strings.Map restituisce s com'è, senza allocare, quando nessuna runa cambia:
// il caso di quasi tutti i testi.
func riparaCP1252(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 0x80 && r <= 0x9f {
			if c := cp1252[r-0x80]; c != 0 {
				return c
			}
			return -1 // non assegnato in CP1252: si butta
		}
		return r
	}, s)
}

func pulisci(s string) string {
	return riparaCP1252(strings.Join(strings.Fields(s), " "))
}
