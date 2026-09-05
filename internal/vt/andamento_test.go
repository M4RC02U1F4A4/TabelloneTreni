package vt

import (
	"context"
	"net/http"
	"os"
	"testing"
)

// Il 2247 è stato catturato in corsa: due fermate servite e quattro ancora da
// fare, che è la sola configurazione in cui si vede la differenza fra un
// orario reale e uno previsto.
func fixtureAndamento(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/andamento-2247.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAndamentoSulTrenoInCorsa(t *testing.T) {
	corpo := fixtureAndamento(t)
	c, chiesti := clienteSu(t, func(w http.ResponseWriter, r *http.Request) { w.Write(corpo) })

	a, err := c.Andamento(context.Background(), "S01700", "2247", 1788645600000)
	if err != nil {
		t.Fatal(err)
	}
	if a == nil {
		t.Fatal("nessun andamento")
	}
	if len(*chiesti) != 1 || (*chiesti)[0] != "/andamentoTreno/S01700/2247/1788645600000" {
		t.Fatalf("percorso chiesto: %v", *chiesti)
	}

	if a.Ritardo != 3 {
		t.Errorf("ritardo = %d, atteso 3", a.Ritardo)
	}
	if a.Stazione != "MILANO LAMBRATE" {
		t.Errorf("ultimo rilevamento = %q", a.Stazione)
	}
	if a.Ora.IsZero() {
		t.Error("l'ora dell'ultimo rilevamento manca")
	}
	if len(a.Fermate) != 6 {
		t.Fatalf("fermate = %d, attese 6", len(a.Fermate))
	}

	// Le prime due servite, le altre no: è la posizione del treno.
	for i, f := range a.Fermate {
		attesaPassata := i < 2
		if f.Passata != attesaPassata {
			t.Errorf("fermata %d (%s): passata = %v, attesa %v", i, f.Nome, f.Passata, attesaPassata)
		}
		if f.Passata && f.Effettiva.IsZero() {
			t.Errorf("fermata %d (%s): passata senza orario reale", i, f.Nome)
		}
		if !f.Passata && !f.Effettiva.IsZero() {
			t.Errorf("fermata %d (%s): non passata ma con orario reale", i, f.Nome)
		}
		if f.Programmata.IsZero() {
			t.Errorf("fermata %d (%s): manca l'orario previsto", i, f.Nome)
		}
	}

	// I codici stazione ci sono, ed è su quelli che si riconosce dove si scende.
	if a.Fermate[0].Codice == "" {
		t.Error("la fermata non porta il codice stazione")
	}
}

// Un treno che ViaggiaTreno non traccia risponde 200 con il corpo vuoto: non è
// un errore, è metà del tabellone in una giornata qualsiasi.
func TestAndamentoCorpoVuoto(t *testing.T) {
	c, _ := clienteSu(t, func(w http.ResponseWriter, r *http.Request) {})

	a, err := c.Andamento(context.Background(), "S01700", "2247", 1)
	if err != nil {
		t.Fatalf("corpo vuoto trattato come errore: %v", err)
	}
	if a != nil {
		t.Fatal("un corpo vuoto non può produrre un andamento")
	}
}

func TestAndamentoErroreHTTP(t *testing.T) {
	c, _ := clienteSu(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})

	if _, err := c.Andamento(context.Background(), "S01700", "2247", 1); err == nil {
		t.Fatal("un 502 deve dare errore")
	}
}

// La stazione dell'ultimo rilevamento vale "--" finché il treno non passa da un
// punto di controllo: è un segnaposto, non il nome di un posto, e non deve
// arrivare fino allo schermo.
func TestSegnapostoDellUltimoRilevamento(t *testing.T) {
	corpo := []byte(`{"ritardo":0,"stazioneUltimoRilevamento":"--","oraUltimoRilevamento":null,"fermate":[]}`)
	c, _ := clienteSu(t, func(w http.ResponseWriter, r *http.Request) { w.Write(corpo) })

	a, err := c.Andamento(context.Background(), "S01700", "1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if a.Stazione != "" {
		t.Errorf("stazione = %q, attesa vuota", a.Stazione)
	}
	if !a.Ora.IsZero() {
		t.Errorf("ora = %v, attesa vuota", a.Ora)
	}
}
