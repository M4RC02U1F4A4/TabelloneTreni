package stations

import (
	"strings"
	"unicode"
)

// Canon riduce il nome di una stazione a una forma confrontabile: maiuscolo,
// senza accenti e senza punteggiatura, con gli spazi compattati.
//
// La punteggiatura sparisce invece di essere conservata perché RFI la usa in
// modo incoerente ("MI.P.GARIBALDI", "MI BOVISA P.", "CASSANO D`ADDA") e la
// emette anche in forme sfuggite più volte all'escaping HTML, per cui lo stesso
// apostrofo arriva come `'`, come "&#39;" o come una catena di questi.
func Canon(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	spazioInSospeso := false
	for _, r := range s {
		r = unicode.ToUpper(deaccenta(r))
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			if spazioInSospeso && b.Len() > 0 {
				b.WriteByte(' ')
			}
			spazioInSospeso = false
			b.WriteRune(r)
		default:
			// Qualsiasi altra cosa — punteggiatura, spazi, apostrofi
			// sopravvissuti all'unescaping — vale come separatore.
			spazioInSospeso = true
		}
	}
	return b.String()
}

var accentate = []rune("ÀÁÂÃÄÅàáâãäåÈÉÊËèéêëÌÍÎÏìíîïÒÓÔÕÖòóôõöÙÚÛÜùúûüÇçÑñ")
var semplici = []rune("AAAAAAaaaaaaEEEEeeeeIIIIiiiiOOOOOoooooUUUUuuuuCcNn")

func deaccenta(r rune) rune {
	if r < unicode.MaxASCII {
		return r
	}
	for i, a := range accentate {
		if a == r {
			return semplici[i]
		}
	}
	return r
}

// Combacia dice se due nomi canonici indicano la stessa stazione, tollerando le
// abbreviazioni con cui RFI tronca i nomi nell'elenco delle fermate.
//
// Il confronto è token per token e richiede che i token siano tanti da una
// parte quanto dall'altra: è questo vincolo a rendere sicuro il confronto per
// prefisso, perché impedisce che "LODI" catturi "LODI VECCHIO". Con lo stesso
// numero di token, ogni token abbreviato deve essere prefisso del suo
// corrispondente: "GAZZADA SCHIAN M" ritrova "GAZZADA SCHIANNO MORAZZONE" e
// "MI BOVISA P" ritrova "MILANO BOVISA POLITECNICO", mentre "MILANO CENTRALE"
// e "MILANO CERTOSA" restano distinti perché CENTRALE non è prefisso di
// CERTOSA né viceversa.
func Combacia(a, b string) bool {
	if a == b {
		return len(a) > 0
	}
	if len(a) < 3 || len(b) < 3 {
		return false
	}
	ta, tb := strings.Fields(a), strings.Fields(b)
	if len(ta) != len(tb) {
		return false
	}
	for i := range ta {
		if !strings.HasPrefix(ta[i], tb[i]) && !strings.HasPrefix(tb[i], ta[i]) {
			return false
		}
	}
	return true
}
