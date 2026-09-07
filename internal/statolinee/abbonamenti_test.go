package statolinee

import (
	"os"
	"path/filepath"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func abbonamento(endpoint string, linee ...string) Abbonamento {
	return Abbonamento{
		Sottoscrizione: webpush.Subscription{
			Endpoint: endpoint,
			Keys:     webpush.Keys{Auth: "YXV0aA", P256dh: "cDI1NmRo"},
		},
		Linee: linee,
	}
}

// Gli abbonamenti devono sopravvivere al riavvio: e' l'unica ragione per cui
// esiste un file, e i riavvii qui sono uno per rilascio.
func TestAbbonamentiSopravvivonoAlRiavvio(t *testing.T) {
	f := filepath.Join(t.TempDir(), "abbonamenti.json")

	a, err := ApriAbbonati(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Registra(abbonamento("https://push.example/uno", "S2", "R16")); err != nil {
		t.Fatal(err)
	}
	if err := a.Registra(abbonamento("https://push.example/due", "S2")); err != nil {
		t.Fatal(err)
	}

	riaperto, err := ApriAbbonati(f)
	if err != nil {
		t.Fatal(err)
	}
	if riaperto.Quanti() != 2 {
		t.Fatalf("abbonamenti = %d, attesi 2", riaperto.Quanti())
	}
	if n := len(riaperto.PerLinea("S2")); n != 2 {
		t.Errorf("S2 = %d abbonati, attesi 2", n)
	}
	if n := len(riaperto.PerLinea("R16")); n != 1 {
		t.Errorf("R16 = %d abbonati, atteso 1", n)
	}
	if n := len(riaperto.PerLinea("S99")); n != 0 {
		t.Errorf("S99 = %d abbonati, atteso nessuno", n)
	}
}

// Spegnere l'ultima campanella arriva come un elenco vuoto, ed e' lo stesso
// gesto di disiscriversi: se restasse registrato, continuerebbe ad arrivare
// una notifica a chi ha appena detto di non volerne piu'.
func TestElencoVuotoCancella(t *testing.T) {
	f := filepath.Join(t.TempDir(), "abbonamenti.json")
	a, _ := ApriAbbonati(f)
	a.Registra(abbonamento("https://push.example/uno", "S2"))

	if err := a.Registra(abbonamento("https://push.example/uno")); err != nil {
		t.Fatal(err)
	}
	if a.Quanti() != 0 {
		t.Fatalf("abbonamenti = %d, atteso nessuno", a.Quanti())
	}
	riaperto, _ := ApriAbbonati(f)
	if riaperto.Quanti() != 0 {
		t.Fatalf("dopo il riavvio = %d, atteso nessuno", riaperto.Quanti())
	}
}

// Registrarsi di nuovo aggiorna le linee invece di duplicare: il telefono
// manda l'elenco intero a ogni tocco di campanella.
func TestRegistrazioneAggiorna(t *testing.T) {
	a, _ := ApriAbbonati("")
	a.Registra(abbonamento("https://push.example/uno", "S2"))
	a.Registra(abbonamento("https://push.example/uno", "R16", "S1"))

	if a.Quanti() != 1 {
		t.Fatalf("abbonamenti = %d, atteso 1", a.Quanti())
	}
	if len(a.PerLinea("S2")) != 0 {
		t.Error("S2 e' rimasta attaccata dopo l'aggiornamento")
	}
	if len(a.PerLinea("R16")) != 1 {
		t.Error("R16 non registrata")
	}
}

// L'endpoint e' aperto a chiunque apra l'applicazione: quello che arriva va
// controllato prima di scriverlo su disco.
func TestRegistrazioniRifiutate(t *testing.T) {
	a, _ := ApriAbbonati("")
	casi := map[string]Abbonamento{
		"endpoint vuoto":     abbonamento("", "S2"),
		"endpoint non https": abbonamento("http://push.example/uno", "S2"),
		"endpoint storto":    abbonamento("non-un-url", "S2"),
		"senza chiavi": {
			Sottoscrizione: webpush.Subscription{Endpoint: "https://push.example/uno"},
			Linee:          []string{"S2"},
		},
		"troppe linee": abbonamento("https://push.example/uno",
			make([]string, MaxLineePerAbbonamento+1)...),
	}
	for nome, ab := range casi {
		t.Run(nome, func(t *testing.T) {
			if err := a.Registra(ab); err == nil {
				t.Fatal("accettato, atteso un rifiuto")
			}
		})
	}
	if a.Quanti() != 0 {
		t.Fatalf("qualcosa e' passato: %d abbonamenti", a.Quanti())
	}
}

// Un file scritto a meta' da un riavvio perderebbe tutti gli abbonati
// insieme: la scrittura passa da un temporaneo e da un rename.
func TestScritturaNonLasciaResidui(t *testing.T) {
	d := t.TempDir()
	a, _ := ApriAbbonati(filepath.Join(d, "abbonamenti.json"))
	for i := range 5 {
		a.Registra(abbonamento("https://push.example/"+string(rune('a'+i)), "S2"))
	}
	voci, err := os.ReadDir(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(voci) != 1 || voci[0].Name() != "abbonamenti.json" {
		var nomi []string
		for _, v := range voci {
			nomi = append(nomi, v.Name())
		}
		t.Fatalf("nella cartella c'e' %v, atteso il solo abbonamenti.json", nomi)
	}
}
