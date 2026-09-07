package vt

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
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

// Il binario che serve è quello dove il treno entra, cioè quello di arrivo.
// Sulla stazione di origine un arrivo non c'è, e lì vale quello di partenza:
// senza questa deroga la prima fermata resterebbe l'unica senza binario,
// proprio quella dove si è in piedi ad aspettare.
func TestBinarioDiArrivoConDerogaSullOrigine(t *testing.T) {
	corpo := fixtureAndamento(t)
	c, _ := clienteSu(t, func(w http.ResponseWriter, r *http.Request) { w.Write(corpo) })

	a, err := c.Andamento(context.Background(), "S01700", "2247", 1788645600000)
	if err != nil {
		t.Fatal(err)
	}
	attesi := map[string]string{
		"MILANO CENTRALE":  "12", // origine: nessun arrivo, vale la partenza
		"MILANO LAMBRATE":  "7",
		"PIOLTELLO LIMITO": "1", // arrivo effettivo 1, previsto 3
		"BERGAMO":          "1 Tronco OVEST",
	}
	for _, f := range a.Fermate {
		atteso, cercata := attesi[f.Nome]
		if !cercata {
			continue
		}
		if f.Binario() != atteso {
			t.Errorf("%s: binario = %q, atteso %q", f.Nome, f.Binario(), atteso)
		}
		delete(attesi, f.Nome)
	}
	if len(attesi) != 0 {
		t.Errorf("fermate non trovate nella risposta: %v", attesi)
	}
}

// Il binario cambiato vale come sul tabellone: serve che ci siano tutti e due i
// valori, altrimenti non si sta confrontando niente. A Pioltello ci sono
// entrambi e differiscono; alle altre fermate manca l'effettivo.
func TestBinarioCambiatoSoloConEntrambiIValori(t *testing.T) {
	corpo := fixtureAndamento(t)
	c, _ := clienteSu(t, func(w http.ResponseWriter, r *http.Request) { w.Write(corpo) })

	a, err := c.Andamento(context.Background(), "S01700", "2247", 1788645600000)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range a.Fermate {
		atteso := f.Nome == "PIOLTELLO LIMITO"
		if f.BinarioCambiato() != atteso {
			t.Errorf("%s: cambiato = %v, atteso %v (previsto %q, effettivo %q)",
				f.Nome, f.BinarioCambiato(), atteso, f.BinarioProgrammato, f.BinarioEffettivo)
		}
	}
}

// Un treno seguito non ha più un tabellone sotto: le coordinate per richiederlo
// deve portarsele il viaggio stesso. Nel corpo di ViaggiaTreno `codOrigine`
// arriva null, quindi vanno tenute quelle con cui si è chiesto.
func TestIlViaggioPortaLeProprieCoordinate(t *testing.T) {
	corpo := fixtureAndamento(t)
	c, _ := clienteSu(t, func(w http.ResponseWriter, r *http.Request) { w.Write(corpo) })

	a, err := c.Andamento(context.Background(), "S01700", "2247", 1788645600000)
	if err != nil {
		t.Fatal(err)
	}
	if a.CodOrigine != "S01700" || a.Numero != "2247" || a.DataPartenza != 1788645600000 {
		t.Errorf("coordinate = %q %q %d", a.CodOrigine, a.Numero, a.DataPartenza)
	}
	if a.Categoria != "REG" || a.Destinazione != "BERGAMO" || a.Origine != "MILANO CENTRALE" {
		t.Errorf("identità = %q %q → %q", a.Categoria, a.Origine, a.Destinazione)
	}
	if a.Arrivato {
		t.Error("dato per arrivato un treno ancora in corsa")
	}
}

// La scheda di un treno seguito si toglie da sola, ma non nell'istante in cui
// il treno arriva: chi lo seguiva è lì per vedere proprio quello.
func TestUnViaggioSiConcludeMezzOraDopoLArrivo(t *testing.T) {
	arrivo := time.Date(2026, 9, 7, 20, 41, 0, 0, time.UTC)
	casi := []struct {
		nome     string
		a        Andamento
		adesso   time.Time
		concluso bool
	}{
		{
			nome:   "in corsa",
			a:      Andamento{Fermate: []Fermata{{Effettiva: arrivo}}},
			adesso: arrivo.Add(3 * time.Hour),
		},
		{
			nome:   "appena arrivato",
			a:      Andamento{Arrivato: true, Fermate: []Fermata{{Effettiva: arrivo}}},
			adesso: arrivo.Add(5 * time.Minute),
		},
		{
			nome:     "arrivato da un pezzo",
			a:        Andamento{Arrivato: true, Fermate: []Fermata{{Effettiva: arrivo}}},
			adesso:   arrivo.Add(45 * time.Minute),
			concluso: true,
		},
		{
			// Non dovrebbe capitare; se capita, tenersi la scheda costa meno
			// che buttarla per una deduzione che non regge.
			nome:   "arrivato senza nemmeno un orario reale",
			a:      Andamento{Arrivato: true, Fermate: []Fermata{{}}},
			adesso: arrivo.Add(3 * time.Hour),
		},
	}
	for _, caso := range casi {
		t.Run(caso.nome, func(t *testing.T) {
			if got := caso.a.Concluso(caso.adesso); got != caso.concluso {
				t.Errorf("concluso = %v, atteso %v", got, caso.concluso)
			}
		})
	}
}
