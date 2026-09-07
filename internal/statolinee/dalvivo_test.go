package statolinee

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/trenord"
)

// TestFiltroControAlternanzaVera interroga Trenord davvero, molte volte di
// fila, e verifica che il registro non annunci cambi che non ci sono.
//
// Sta fuori dai test normali perché dipende dalla rete, ma è l'unico che
// misura la cosa per cui il filtro esiste: lo stesso indirizzo risponde da
// backend che non concordano, a caso a ogni richiesta. Senza filtro, una linea
// fra le instabili produce un "cambio" ogni due letture circa.
//
//	TRENORD_LIVE=1 go test ./internal/statolinee/ -run Alternanza -v
func TestFiltroControAlternanzaVera(t *testing.T) {
	if os.Getenv("TRENORD_LIVE") == "" {
		t.Skip("richiede rete: TRENORD_LIVE=1 per eseguirlo")
	}
	const letture = 12
	c := trenord.NewClient()

	// Quali linee alternino cambia di ora in ora: si prende la prima che in
	// questo momento lo sta facendo, invece di fissarne una che domani è
	// tranquilla e trasformerebbe il test in un "passa sempre".
	candidate := []string{"S3", "S8", "S12", "S13", "R4", "R8", "S5", "RE_13"}

	for _, codice := range candidate {
		r := NuovoRegistro()
		var annunci int
		visti := map[string]int{}

		for i := range letture {
			ctx, annulla := context.WithTimeout(context.Background(), 30*time.Second)
			d, err := c.Dettaglio(ctx, codice)
			annulla()
			if err != nil {
				t.Fatalf("%s lettura %d: %v", codice, i, err)
			}
			visti[d.Stato.String()+"@"+d.Aggiornato.Format("15:04")]++
			if n := r.MettiDettaglio(codice, "prova", d); n.Cambio != nil {
				annunci++
				t.Logf("%s lettura %d: annunciato %s -> %s", codice, i, n.Cambio.Prima, n.Cambio.Linea.Stato)
			}
		}
		if len(visti) < 2 {
			t.Logf("%s: concorde (%v), provo la prossima", codice, visti)
			continue
		}

		t.Logf("%s alterna: %v", codice, visti)
		// La prima lettura non annuncia mai. Oltre quella, un cambio vero in
		// mezzo minuto è possibile ma raro; due o più sono l'alternanza che
		// passa attraverso il filtro.
		if annunci > 1 {
			t.Errorf("%s: annunci = %d in %d letture ravvicinate: l'alternanza sta passando",
				codice, annunci, letture)
		} else {
			t.Logf("%s: annunci = %d — l'alternanza è stata assorbita", codice, annunci)
		}
		return
	}
	t.Skip("in questo momento nessuna candidata alterna: niente da filtrare")
}
