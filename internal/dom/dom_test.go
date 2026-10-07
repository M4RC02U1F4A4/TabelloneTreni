package dom

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// I valori attesi sono quelli che davano le copie di testo, attr, haClasse e
// trova prima che finissero qui: rfi e trenord li leggono già così, e un solo
// spazio in più o in meno cambierebbe quello che i loro parser restituiscono.

const frammento = `<div id="a" class="x  y"><p>uno <b>due</b></p>tre<!-- nota --><img alt="i"><span class="y z">quattro</span>
  cinque</div>`

func TestFrammento(t *testing.T) {
	doc, err := html.Parse(strings.NewReader(frammento))
	if err != nil {
		t.Fatal(err)
	}
	div := Trova(doc, func(n *html.Node) bool { return Attr(n, "id") == "a" })
	if div == nil {
		t.Fatal("div non trovato")
	}
	// Uno spazio dopo ogni nodo di testo, commenti esclusi, nell'ordine del
	// documento.
	if got, want := Testo(div), "uno  due tre quattro \n  cinque "; got != want {
		t.Errorf("Testo = %q, atteso %q", got, want)
	}
	if got := Attr(div, "class"); got != "x  y" {
		t.Errorf("Attr class = %q", got)
	}
	if got := Attr(div, "alt"); got != "" {
		t.Errorf("Attr mancante = %q", got)
	}
	for c, want := range map[string]bool{"x": true, "y": true, "x  y": false, "z": false} {
		if got := HaClasse(div, c); got != want {
			t.Errorf("HaClasse(%q) = %v, atteso %v", c, got, want)
		}
	}
	// Trova guarda anche il nodo da cui parte.
	if got := Trova(div, func(n *html.Node) bool { return HaClasse(n, "y") }); got != div {
		t.Errorf("Trova y = %v, atteso il div stesso", got)
	}
	span := Trova(div, func(n *html.Node) bool { return HaClasse(n, "z") })
	if span == nil || span.Data != "span" || Testo(span) != "quattro " {
		t.Errorf("Trova z = %v", span)
	}
	img := Trova(div, func(n *html.Node) bool { return n.Data == "img" })
	if img == nil || Attr(img, "alt") != "i" {
		t.Errorf("Trova img = %v", img)
	}
}

// TestFixtureRFI confronta, elemento per elemento, la pagina vera di RFI con
// l'impronta presa dalle vecchie funzioni: tag, id, classe, due HaClasse e
// tutto il Testo.
func TestFixtureRFI(t *testing.T) {
	f, err := os.Open("../rfi/testdata/partenze-1715.html")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	doc, err := html.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	n := 0
	for x := range doc.Descendants() {
		if x.Type != html.ElementNode {
			continue
		}
		n++
		fmt.Fprintf(h, "%s|%s|%s|%v|%v|%s\n", x.Data, Attr(x, "id"), Attr(x, "class"),
			HaClasse(x, "testoinfoaggiuntive"), HaClasse(x, "marqueeinfosupp"), Testo(x))
	}
	if n != 945 {
		t.Errorf("elementi = %d, attesi 945", n)
	}
	if got, want := fmt.Sprintf("%x", h.Sum(nil)), "f9c76c873a604bc99f7cbbda2394ac98eb42970c8310c1e7cbe760f41dd5b96a"; got != want {
		t.Errorf("impronta = %s, attesa %s", got, want)
	}
	if got := len(Testo(doc)); got != 66305 {
		t.Errorf("len(Testo(doc)) = %d, attesa 66305", got)
	}
	p := Trova(doc, func(n *html.Node) bool { return HaClasse(n, "testoinfoaggiuntive") })
	if got := strings.Join(strings.Fields(Testo(p)), " "); got != "FERMA A:MILANO CENTRALE (21:37)" {
		t.Errorf("primo testoinfoaggiuntive = %q", got)
	}
	if img := Trova(doc, func(n *html.Node) bool { return n.Data == "img" }); Attr(img, "alt") != "Aggiorna" {
		t.Errorf("prima img alt = %q", Attr(img, "alt"))
	}
}
