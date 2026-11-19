package statolinee

import (
	"bytes"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// I test parlano con servizi push finti, su push.example o su un httptest in
// TLS: dell'allowlist resta il solo HTTPS. L'allowlist vera la prova
// TestSoloServiziPushVeri.
func TestMain(m *testing.M) {
	endpointAmmesso = func(u *url.URL) bool { return u.Scheme == "https" }
	os.Exit(m.Run())
}

// Chiunque può abbonarsi, e l'endpoint è l'indirizzo a cui il servizio farà
// POST firmate: fuori dai servizi push dei browser sarebbe un SSRF.
func TestSoloServiziPushVeri(t *testing.T) {
	for e, atteso := range map[string]bool{
		"https://fcm.googleapis.com/fcm/send/abc":                true,
		"https://updates.push.services.mozilla.com/wpush/v2/abc": true,
		"https://web.push.apple.com/QGx":                         true,
		"https://wns2-db5p.notify.windows.com/w/?token=abc":      true,
		"https://FCM.googleapis.com/fcm/send/abc":                true,
		"https://fcm.googleapis.com:443/fcm/send/abc":            true,
		"http://fcm.googleapis.com/fcm/send/abc":                 false,
		"https://fcm.googleapis.com:8443/x":                      false,
		"https://utente@fcm.googleapis.com/x":                    false,
		"https://fcm.googleapis.com.example/x":                   false,
		"https://evilpush.apple.com/x":                           false,
		"https://kubernetes.default.svc/api":                     false,
		"https://127.0.0.1/x":                                    false,
		"https://statolinee:8081/push/abbonamenti":               false,
	} {
		u, err := url.Parse(e)
		if err != nil {
			t.Fatal(err)
		}
		if endpointPushNoto(u) != atteso {
			t.Errorf("%s: ammesso = %v, atteso %v", e, !atteso, atteso)
		}
	}
}

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
		"linea inventata": abbonamento("https://push.example/uno", "S2\nfalso log"),
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

// Una scrittura su disco fallita non ha nessuno a cui tornare indietro, ma
// deve almeno finire nel log: in silenzio, il volume pieno o sparito si scopre
// solo al riavvio, quando gli abbonamenti non ci sono più.
func TestScritturaFallitaFinisceNelLog(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	a, err := ApriAbbonati(filepath.Join(t.TempDir(), "no", "such", "dir", "abbonati.json"))
	if err != nil {
		t.Fatal(err)
	}
	const endpoint = "https://push.example/uno"
	// Registra l'errore lo restituisce già: l'abbonamento resta in memoria.
	_ = a.Registra(abbonamento(endpoint, "S2"))
	buf.Reset()

	a.SegnaVisto(endpoint, map[string]Visto{})
	if !strings.Contains(buf.String(), "scrittura") {
		t.Fatalf("log = %q, attesa la scrittura fallita", buf.String())
	}
}
