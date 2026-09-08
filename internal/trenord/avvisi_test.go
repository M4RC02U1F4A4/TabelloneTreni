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

// La pagina di una linea tiene due elenchi separati — "STATO DELLA LINEA" e
// "AVVISI" — e la differenza va portata su, perché è quella che decide cosa
// finisce sotto gli occhi e cosa fa suonare il telefono.
//
// La RE_5 è stata catturata l'8 settembre 2026 alle 19:04, con tre
// comunicazioni di circolazione della sera e due avvisi programmati: è la sola
// configurazione in cui si vede che le due liste arrivano mescolate.
func TestLeDueSezioniSiDistinguono(t *testing.T) {
	f, err := os.Open("testdata/line-details-RE_5.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := ParseDettaglio(f)
	if err != nil {
		t.Fatal(err)
	}

	var circolazione, avvisi, ignote int
	for _, a := range d.Avvisi {
		switch a.Sezione {
		case SezioneCircolazione:
			circolazione++
		case SezioneAvvisi:
			avvisi++
		default:
			ignote++
			t.Errorf("avviso senza sezione: %.60s", a.Testo)
		}
	}
	if circolazione != 3 {
		t.Errorf("circolazione = %d, attese 3", circolazione)
	}
	if avvisi != 2 {
		t.Errorf("avvisi = %d, attesi 2", avvisi)
	}

	// I tre della circolazione sono i treni della sera; i due avvisi sono le
	// variazioni d'orario e lo sciopero, pubblicati giorni prima.
	for _, a := range d.Avvisi {
		vecchio := a.Data.Before(time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC))
		if a.Sezione == SezioneCircolazione && vecchio {
			t.Errorf("circolazione datata %s: %.60s", a.Data.Format("2/1"), a.Testo)
		}
		if a.Sezione == SezioneAvvisi && !vecchio {
			t.Errorf("avviso di oggi: %.60s", a.Testo)
		}
	}
}

// Le schede del carosello che non sono comunicazioni — biglietti, distributori
// — non hanno testo e restano fuori, come prima.
func TestLeSchedeDelCaroselloRestanoFuori(t *testing.T) {
	f, err := os.Open("testdata/line-details-RE_5.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := ParseDettaglio(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Avvisi) != 5 {
		t.Fatalf("avvisi = %d, attesi 5: nel carosello ce ne sono sette, due sono schede", len(d.Avvisi))
	}
}

// Uno sciopero si riconosce dalla parola, perché la sorgente non ha un campo
// che lo dica e la sezione non aiuta: sta fra gli avvisi programmati, in mezzo
// alle variazioni d'orario. Nella fixture della RE_5 ce n'è esattamente uno,
// indetto il primo settembre per il 7 e l'8 — sei giorni prima, che è il motivo
// per cui vale la pena avvisare.
func TestLoScioperoSiRiconosce(t *testing.T) {
	f, err := os.Open("testdata/line-details-RE_5.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := ParseDettaglio(f)
	if err != nil {
		t.Fatal(err)
	}

	var scioperi []Avviso
	for _, a := range d.Avvisi {
		if a.Sciopero {
			scioperi = append(scioperi, a)
		}
	}
	if len(scioperi) != 1 {
		t.Fatalf("scioperi = %d, atteso 1: %+v", len(scioperi), scioperi)
	}
	s := scioperi[0]
	if !strings.Contains(s.Testo, "sciopero nazionale") {
		t.Errorf("non è l'avviso atteso: %.80s", s.Testo)
	}
	// Sta fra i programmati, ed è proprio il punto: filtrando per sezione si
	// perderebbe.
	if s.Sezione != SezioneAvvisi {
		t.Errorf("sezione = %d, attesa quella degli avvisi programmati", s.Sezione)
	}
	// E le variazioni d'orario, che stanno nella stessa sezione, non sono uno
	// sciopero: se lo fossero, il cartello rosso comparirebbe per tre mesi.
	for _, a := range d.Avvisi {
		if a.Sciopero == strings.Contains(a.Testo, "variazioni") && strings.Contains(a.Testo, "variazioni") {
			t.Errorf("le variazioni d'orario passano per sciopero: %.80s", a.Testo)
		}
	}
}

// Tutte le forme della parola, e anche quando compare solo nel nome del PDF.
func TestLeFormeDellaParolaSciopero(t *testing.T) {
	casi := map[string]bool{
		"I sindacati hanno indetto uno sciopero nazionale":         true,
		"Scioperi del personale previsti per lunedì":               true,
		"Il personale scioperano dalle 9 alle 17":                  true,
		"consultare AvvisoTrenord_2026_186__Sciopero_7-8.pdf":      true,
		"Dal 24 agosto i seguenti treni subiscono variazioni":      false,
		"Il treno 2536 non è ancora partito per un guasto tecnico": false,
		"Circolazione rallentata per un guasto agli impianti":      false,
	}
	for testo, atteso := range casi {
		if got := parlaDiSciopero.MatchString(testo); got != atteso {
			t.Errorf("%.50s → %v, atteso %v", testo, got, atteso)
		}
	}
}
