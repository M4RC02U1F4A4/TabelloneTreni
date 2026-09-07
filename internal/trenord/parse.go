package trenord

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

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
			case n.Data == "ul" && haClasse(n, "new_line"):
				// Il titolo del gruppo è dentro la <ul>, non prima: da qui in
				// giù tutte le linee appartengono a questo gruppo.
				gruppo = titoloGruppo(n)
			case n.Data == "a" && attr(n, "data-code") != "":
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
		Codice: strings.TrimSpace(attr(a, "data-code")),
		Nome:   pulisci(attr(a, "data-name")),
		Gruppo: gruppo,
	}
	// Il nome sta nell'attributo e anche nel testo della voce: l'attributo è
	// quello che Trenord usa per il proprio filtro di ricerca, il testo porta
	// spazi di impaginazione. Se l'attributo manca si ripiega sul testo.
	if l.Nome == "" {
		l.Nome = pulisci(testo(a))
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

// statoDi legge il semaforo dalle classi del div .status-line.
func statoDi(n *html.Node) (Stato, bool) {
	if n.Type == html.ElementNode && haClasse(n, "status-line") {
		switch {
		case haClasse(n, "danger"):
			return Grave, true
		case haClasse(n, "critical"):
			return Critico, true
		case haClasse(n, "green-line"):
			return Regolare, true
		}
		return 0, false
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if s, ok := statoDi(c); ok {
			return s, true
		}
	}
	return 0, false
}

func titoloGruppo(ul *html.Node) string {
	for c := ul.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "p" && haClasse(c, "title-line") {
			return pulisci(testo(c))
		}
	}
	return ""
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

func pulisci(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
