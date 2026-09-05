package vt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// clienteSu punta il client a un server di prova e registra i percorsi chiesti.
func clienteSu(t *testing.T, h http.HandlerFunc) (*Client, *[]string) {
	t.Helper()
	var chiesti []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chiesti = append(chiesti, r.URL.Path)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	c := NewClient()
	c.base = srv.URL
	return c, &chiesti
}

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/partenze-S01645.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRitardiSullaRispostaVera(t *testing.T) {
	corpo := fixture(t)
	c, _ := clienteSu(t, func(w http.ResponseWriter, r *http.Request) { w.Write(corpo) })

	r, err := c.Ritardi(context.Background(), "S01645", false)
	if err != nil {
		t.Fatal(err)
	}
	// 24 treni nella risposta, 14 dei quali non ancora partiti: restano i 10
	// che qualcuno ha davvero misurato.
	if len(r) != 10 {
		t.Fatalf("misure = %d, attese 10", len(r))
	}

	casi := []struct {
		treno  string
		minuti int
	}{
		{"3087", 2},
		// Un ritardo grosso: nella stessa risposta il tabellone ne dichiarava
		// 70, e sono i venticinque minuti di differenza il motivo per cui le
		// due letture restano separate invece che scegliere la migliore.
		{"9808", 95},
		// Zero misurato: il treno è stato rilevato e va in orario. È il caso
		// che distingue una misura dal valore di partenza del campo.
		{"25080", 0},
		// In anticipo: ViaggiaTreno lo scrive "in orario", ma il numero è -1 e
		// quello che si mostra è il numero.
		{"2979", -1},
	}
	for _, caso := range casi {
		got, ok := r[caso.treno]
		if !ok {
			t.Errorf("treno %s: nessuna misura", caso.treno)
			continue
		}
		if got.Minuti != caso.minuti {
			t.Errorf("treno %s: %d minuti, attesi %d", caso.treno, got.Minuti, caso.minuti)
		}
	}

	// 24880 nella risposta c'è, con ritardo 0, ma non è mai partito: quello
	// zero non è una misura e non deve arrivare fino allo schermo.
	if _, ok := r["24880"]; ok {
		t.Error("il treno non partito 24880 non deve avere una misura")
	}
}

func TestNonPartitoNonEUnaMisura(t *testing.T) {
	casi := []struct {
		nome   string
		treno  treno
		atteso bool
	}{
		{"non partito con lo zero di default", treno{Ritardo: 0, CompRitardo: []string{"non partito"}}, false},
		{"rilevato e puntuale", treno{Ritardo: 0, CompRitardo: []string{"in orario"}}, true},
		{"rilevato e in ritardo", treno{Ritardo: 4, CompRitardo: []string{"ritardo 4 min."}}, true},
		{"in anticipo", treno{Ritardo: -1, CompRitardo: []string{"in orario"}}, true},
		// Un ritardo diverso da zero è una misura comunque: se domani comparisse
		// una dicitura che qui non si conosce, la regola non deve rompersi.
		{"dicitura sconosciuta ma numero valorizzato", treno{Ritardo: 7, CompRitardo: []string{"boh"}}, true},
		// Senza dicitura, invece, uno zero non si può distinguere dal default:
		// meglio nessuna pastiglia che una puntualità che nessuno ha visto.
		{"zero senza dicitura", treno{Ritardo: 0}, false},
	}
	for _, caso := range casi {
		if got := rilevato(caso.treno); got != caso.atteso {
			t.Errorf("%s: rilevato = %v, atteso %v", caso.nome, got, caso.atteso)
		}
	}
}

func TestArriviEPartenzeSonoDuePercorsi(t *testing.T) {
	c, chiesti := clienteSu(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("[]")) })

	if _, err := c.Ritardi(context.Background(), "S01645", false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Ritardi(context.Background(), "S01645", true); err != nil {
		t.Fatal(err)
	}
	if len(*chiesti) != 2 {
		t.Fatalf("richieste = %d, attese 2", len(*chiesti))
	}
	if !strings.HasPrefix((*chiesti)[0], "/partenze/S01645/") {
		t.Errorf("percorso partenze = %q", (*chiesti)[0])
	}
	if !strings.HasPrefix((*chiesti)[1], "/arrivi/S01645/") {
		t.Errorf("percorso arrivi = %q", (*chiesti)[1])
	}
}

// Una stazione senza partenze risponde 200 con il corpo vuoto, non con "[]":
// è una risposta valida che vale "nessuna misura", non un errore.
func TestCorpoVuoto(t *testing.T) {
	c, _ := clienteSu(t, func(w http.ResponseWriter, r *http.Request) {})

	r, err := c.Ritardi(context.Background(), "S01645", false)
	if err != nil {
		t.Fatalf("corpo vuoto trattato come errore: %v", err)
	}
	if len(r) != 0 {
		t.Fatalf("misure = %d, attese 0", len(r))
	}
}

func TestErroreHTTP(t *testing.T) {
	c, _ := clienteSu(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	if _, err := c.Ritardi(context.Background(), "S01645", false); err == nil {
		t.Fatal("un 500 deve dare errore")
	}
}
