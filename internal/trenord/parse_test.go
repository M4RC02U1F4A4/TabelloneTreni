package trenord

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// La fixture è la risposta vera dell'endpoint, catturata il 7 settembre 2026:
// 65 linee in cinque gruppi, di cui sei con criticità. Contiene anche lo
// script che la pagina esegue per rileggersi i propri semafori, dove le parole
// "critical" e "danger" compaiono come testo — se il parser tornasse a
// guardare le stringhe invece dei nodi, i conti qui sotto non tornerebbero.
func TestParseFixture(t *testing.T) {
	f, err := os.Open("testdata/shoulder-lines.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	linee, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(linee) != 65 {
		t.Fatalf("linee = %d, attese 65", len(linee))
	}

	gruppi := map[string]int{}
	visti := map[string]bool{}
	var critiche []string
	for _, l := range linee {
		if visti[l.Codice] {
			// Un codice ripetuto renderebbe ambigua la campanellina: la
			// preferenza salvata punterebbe a due righe diverse.
			t.Errorf("codice %q ripetuto", l.Codice)
		}
		visti[l.Codice] = true
		gruppi[l.Gruppo]++

		if l.Nome == "" {
			t.Errorf("linea %s senza nome", l.Codice)
		}
		if l.Stato != Regolare {
			critiche = append(critiche, l.Codice+":"+l.Stato.String())
		}
	}

	atteso := map[string]int{
		"REGIO EXPRESS": 10, "MALPENSA EXPRESS": 2, "REGIONALI": 34,
		"LINEE SUBURBANE": 14, "LINEE TRANSFRONTALIERE": 5,
	}
	for g, n := range atteso {
		if gruppi[g] != n {
			t.Errorf("gruppo %q = %d linee, attese %d", g, gruppi[g], n)
		}
	}
	if len(gruppi) != len(atteso) {
		t.Errorf("gruppi = %v, attesi %d", gruppi, len(atteso))
	}

	sort.Strings(critiche)
	const attese = "R16:critico R34:critico S19:critico S2:critico S4:critico S9:critico"
	if got := strings.Join(critiche, " "); got != attese {
		t.Errorf("non regolari:\n  ho   %s\n  attese %s", got, attese)
	}
}

// Il nome della linea va preso dall'attributo, non dal testo della voce, che
// arriva con gli spazi dell'impaginazione attaccati.
func TestNomeSenzaSpaziDiImpaginazione(t *testing.T) {
	f, err := os.Open("testdata/shoulder-lines.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	linee, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range linee {
		if l.Codice == "S2" {
			if l.Nome != "Seveso-Milano Passante-Milano Rogoredo" || l.Gruppo != "LINEE SUBURBANE" {
				t.Fatalf("S2 = %+v", l)
			}
			return
		}
	}
	t.Fatal("S2 non trovata")
}

// Il terzo stato non c'è nel campione — capita di rado — ma la pagina stessa
// lo cerca nel proprio JavaScript, quindi esiste e va letto.
func TestStatoGrave(t *testing.T) {
	const frammento = `<ul class="new_line"><p class="title-line"><b>REGIONALI</b></p><li>
	  <a data-code="R99" data-name="Prova-Prova">
	    <div class="  status-line no-margin danger wrapper"><div class="tooltip">Circolazione con gravi criticità</div></div>
	  </a></li></ul>`

	linee, err := parseHTML(strings.NewReader(frammento))
	if err != nil {
		t.Fatal(err)
	}
	if len(linee) != 1 || linee[0].Stato != Grave {
		t.Fatalf("linee = %+v, atteso un solo stato grave", linee)
	}
}

// Una linea senza semaforo va scartata, non mostrata come regolare: se Trenord
// cambia il markup dello stato, il posto giusto dove accorgersene è qui, non
// sul telefono di chi crede che il suo treno vada.
func TestLineaSenzaStatoScartata(t *testing.T) {
	const frammento = `<ul class="new_line"><p class="title-line"><b>REGIONALI</b></p><li>
	  <a data-code="R1" data-name="Con-Stato"><div class="status-line green-line"></div></a></li><li>
	  <a data-code="R2" data-name="Senza-Stato"><div class="crop"><p>Senza-Stato</p></div></a></li></ul>`

	linee, err := parseHTML(strings.NewReader(frammento))
	if err != nil {
		t.Fatal(err)
	}
	if len(linee) != 1 || linee[0].Codice != "R1" {
		t.Fatalf("linee = %+v, attesa la sola R1", linee)
	}
}

// Un elenco senza linee è un errore, non un elenco vuoto: servito così com'è,
// cancellerebbe la sezione invece di segnalare il guasto. Il caso concreto è
// il 403 dell'anti-bot, che arriva come JSON ben formato.
func TestParseRisposteInutilizzabili(t *testing.T) {
	casi := map[string]string{
		"403 dell'anti-bot": `{"code":"403","message":"Forbidden"}`,
		"elenco vuoto":      `{"message":"<div class=\"list result\"></div>"}`,
		"non è JSON":        `<html><body>Errore</body></html>`,
	}
	for nome, corpo := range casi {
		t.Run(nome, func(t *testing.T) {
			if _, err := Parse(strings.NewReader(corpo)); err == nil {
				t.Fatal("attesa una segnalazione di errore")
			}
		})
	}
}
