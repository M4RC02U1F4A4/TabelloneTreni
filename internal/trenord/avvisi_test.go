package trenord

import (
	"os"
	"strings"
	"testing"
	"time"
)

// La fixture è la risposta vera del dettaglio della S2, catturata il 7
// settembre 2026: due avvisi, dei lavori in corso fino a dicembre e uno
// sciopero. Sono le due cose per cui questo endpoint vale la pena — il
// bollino da solo direbbe "regolare" in entrambi i casi.
func TestParseDettaglio(t *testing.T) {
	f, err := os.Open("testdata/line-details-S2.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	d, err := ParseDettaglio(f)
	if err != nil {
		t.Fatal(err)
	}
	avvisi := d.Avvisi
	if len(avvisi) != 2 {
		t.Fatalf("avvisi = %d, attesi 2: %+v", len(avvisi), avvisi)
	}

	atteso := time.Date(2026, 8, 24, 16, 1, 0, 0, time.UTC)
	if !avvisi[0].Data.Equal(atteso) {
		t.Errorf("data = %v, attesa %v", avvisi[0].Data, atteso)
	}
	if !strings.Contains(avvisi[0].Testo, "lavori di potenziamento infrastrutturale") {
		t.Errorf("primo avviso: %q", avvisi[0].Testo)
	}
	// Lo sciopero arriva da qui e non da una fonte a parte: e' la ragione per
	// cui non serve andarlo a cercare altrove.
	if !strings.Contains(avvisi[1].Testo, "sciopero") {
		t.Errorf("secondo avviso: %q", avvisi[1].Testo)
	}
	// Il testo non deve portarsi dietro il markup nè gli spazi del template.
	for _, a := range avvisi {
		if strings.Contains(a.Testo, "<") || strings.Contains(a.Testo, "  ") {
			t.Errorf("testo sporco: %q", a.Testo)
		}
	}
}

// Una linea senza comunicazioni ha il carosello vuoto, e non è un errore.
func TestAvvisiAssenti(t *testing.T) {
	const vuoto = `{"message":"<div class=\"carousel-line owl-carousel\"></div>"}`
	d, err := ParseDettaglio(strings.NewReader(vuoto))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Avvisi) != 0 {
		t.Fatalf("avvisi = %+v, atteso nessuno", d.Avvisi)
	}
}

// Una risposta senza carosello invece è markup che non riconosciamo più, ed è
// diverso da "questa linea non ha avvisi": la prima cosa va vista, la seconda
// è la normalità.
func TestAvvisiSenzaCarosello(t *testing.T) {
	casi := map[string]string{
		"pagina estranea": `{"message":"<div class=\"altro\"></div>"}`,
		"403 anti-bot":    `{"code":"403","message":"Forbidden"}`,
	}
	for nome, corpo := range casi {
		t.Run(nome, func(t *testing.T) {
			if _, err := ParseDettaglio(strings.NewReader(corpo)); err == nil {
				t.Fatal("attesa una segnalazione di errore")
			}
		})
	}
}

// Un avviso con la data storta vale comunque: perderlo per una data
// significherebbe non dire di uno sciopero perché non si sa quando è stato
// annunciato.
func TestAvvisoConDataStorta(t *testing.T) {
	const c = `{"message":"<div class=\"carousel-line\"><div class=\"item info\">` +
		`<span class=\"news-date\">non-una-data</span>` +
		`<div class=\"body-texts\"><p>Sciopero il 12 dicembre.</p></div></div></div>"}`
	d, err := ParseDettaglio(strings.NewReader(c))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Avvisi) != 1 || d.Avvisi[0].Testo != "Sciopero il 12 dicembre." {
		t.Fatalf("avvisi = %+v", d.Avvisi)
	}
	if !d.Avvisi[0].Data.IsZero() {
		t.Errorf("data = %v, attesa vuota", d.Avvisi[0].Data)
	}
}

// Trenord scrive le comunicazioni a mano, spesso incollando da Word, e i byte
// tipografici di Windows-1252 arrivano non convertiti: sul telefono diventano
// quadratini in mezzo alle parole. Nella fixture c'e' "dell'8 settembre"
// scritto proprio cosi'.
func TestApostrofoDiWord(t *testing.T) {
	f, err := os.Open("testdata/line-details-S2.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := ParseDettaglio(f)
	if err != nil {
		t.Fatal(err)
	}

	var sciopero string
	for _, a := range d.Avvisi {
		if strings.Contains(a.Testo, "sciopero") {
			sciopero = a.Testo
		}
	}
	if sciopero == "" {
		t.Fatal("avviso dello sciopero non trovato")
	}
	if !strings.Contains(sciopero, "dell\u20198 settembre") {
		t.Errorf("apostrofo non riparato: %q", sciopero)
	}
	for _, r := range sciopero {
		if r >= 0x80 && r <= 0x9f {
			t.Errorf("carattere di controllo U+%04X rimasto nel testo", r)
		}
	}
}

func TestRiparaCP1252(t *testing.T) {
	casi := map[string]string{
		"dell\u00928 settembre":  "dell\u20198 settembre",
		"\u0093virgolette\u0094": "\u201cvirgolette\u201d",
		"trattino \u0096 lungo":  "trattino \u2013 lungo",
		"niente da riparare":     "niente da riparare",
		"buttato \u0081 via":     "buttato  via",
	}
	for dentro, atteso := range casi {
		if got := riparaCP1252(dentro); got != atteso {
			t.Errorf("%q -> %q, atteso %q", dentro, got, atteso)
		}
	}
}

// L'orario di aggiornamento e' l'unico modo per riconoscere una risposta
// vecchia: lo stesso indirizzo, interrogato due volte, risponde da backend
// diversi che non concordano. Senza questo campo non c'e' niente da
// confrontare, e le notifiche diventano un lancio di moneta.
func TestDettaglioPortaStatoEOrario(t *testing.T) {
	f, err := os.Open("testdata/line-details-S2.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := ParseDettaglio(f)
	if err != nil {
		t.Fatal(err)
	}
	if d.Stato != Regolare {
		t.Errorf("stato = %v, atteso regolare", d.Stato)
	}
	atteso := time.Date(2026, 9, 7, 14, 44, 0, 0, time.UTC)
	if !d.Aggiornato.Equal(atteso) {
		t.Errorf("aggiornato = %v, atteso %v", d.Aggiornato, atteso)
	}
}
