// Package dom raccoglie le quattro letture dell'albero HTML che servono a chi
// legge le pagine di RFI e di Trenord. Erano copiate in quattro file, e in
// genstations testo non lasciava lo spazio in fondo.
package dom

import (
	"slices"
	"strings"

	"golang.org/x/net/html"
)

// Attr restituisce il valore dell'attributo, vuoto se manca.
func Attr(n *html.Node, nome string) string {
	for _, a := range n.Attr {
		if a.Key == nome {
			return a.Val
		}
	}
	return ""
}

// HaClasse dice se c è una delle classi del nodo.
func HaClasse(n *html.Node, c string) bool {
	return slices.Contains(strings.Fields(Attr(n, "class")), c)
}

// Testo è il testo dei discendenti, uno spazio dopo ogni pezzo: chi lo usa
// compatta poi gli spazi, e senza quello due celle attaccate diventerebbero
// una parola sola.
func Testo(n *html.Node) string {
	var b strings.Builder
	for d := range n.Descendants() {
		if d.Type == html.TextNode {
			b.WriteString(d.Data)
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// Trova restituisce il primo elemento, n compreso, che soddisfa la condizione,
// nell'ordine del documento.
func Trova(n *html.Node, ok func(*html.Node) bool) *html.Node {
	if n.Type == html.ElementNode && ok(n) {
		return n
	}
	for d := range n.Descendants() {
		if d.Type == html.ElementNode && ok(d) {
			return d
		}
	}
	return nil
}
