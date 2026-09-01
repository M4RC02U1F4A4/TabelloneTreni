package rfi

import (
	"os"
	"strings"
	"testing"
)

// Le pagine in testdata sono tabelloni reali salvati il 2026-09-01, con le sole
// GIF base64 dei loghi troncate (il parser ne legge solo l'attributo alt). Se
// uno di questi test si rompe senza che sia cambiato il codice, è cambiato il
// markup di RFI: riscaricare le pagine e confrontare.
func carica(t *testing.T, nome string, arrivals bool) *Board {
	t.Helper()
	f, err := os.Open("testdata/" + nome)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, err := Parse(f, 1715, arrivals)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParsePartenze(t *testing.T) {
	b := carica(t, "partenze-1715.html", false)

	if b.Station != "MILANO PORTA GARIBALDI" {
		t.Errorf("stazione = %q", b.Station)
	}
	if len(b.Trains) != 40 {
		t.Fatalf("treni = %d, attesi 40", len(b.Trains))
	}

	// Ogni riga deve avere almeno numero, orario e destinazione: sono i tre
	// campi senza i quali la riga non è mostrabile.
	for i, tr := range b.Trains {
		if tr.Number == "" || tr.Time == "" || tr.Terminus == "" {
			t.Errorf("riga %d incompleta: %+v", i, tr)
		}
	}

	var conFermate, cancellati int
	for _, tr := range b.Trains {
		if len(tr.Stops) > 0 {
			conFermate++
		}
		if tr.Cancelled {
			cancellati++
		}
	}
	// Le fermate sono il presupposto del filtro per destinazione: se il markup
	// del popup cambia, questo conteggio crolla ed è il primo segnale.
	if conFermate < 30 {
		t.Errorf("solo %d treni su 40 con fermate: popup non più leggibile?", conFermate)
	}
	if cancellati == 0 {
		t.Log("nessun treno cancellato in questo tabellone: il caso non è coperto qui")
	}
}

func TestParseRigaNota(t *testing.T) {
	b := carica(t, "partenze-1715.html", false)

	var tr *Train
	for i := range b.Trains {
		if b.Trains[i].Number == "24177" {
			tr = &b.Trains[i]
		}
	}
	if tr == nil {
		t.Fatal("treno 24177 non trovato")
	}
	if tr.Carrier != "TRENORD" {
		t.Errorf("vettore = %q, atteso TRENORD", tr.Carrier)
	}
	if tr.Category != "S1" {
		t.Errorf("categoria = %q, attesa S1", tr.Category)
	}
	if tr.Terminus != "LODI" || tr.Time != "21:44" || tr.Delay != 43 || tr.Platform != "1 SOT" {
		t.Errorf("riga = %+v", *tr)
	}
	if len(tr.Stops) != 12 {
		t.Fatalf("fermate = %d, attese 12: %+v", len(tr.Stops), tr.Stops)
	}
	// La prima e l'ultima bastano: se la regexp sbaglia i confini, sono queste
	// due a rompersi per prime.
	if tr.Stops[0] != (Stop{Name: "MI REPUBBLICA", Time: "21:46"}) {
		t.Errorf("prima fermata = %+v", tr.Stops[0])
	}
	if tr.Stops[11] != (Stop{Name: "LODI", Time: "22:37"}) {
		t.Errorf("ultima fermata = %+v", tr.Stops[11])
	}
}

// Un ritardo non sempre è un numero: RFI scrive anche "Cancellato" o "RITARDO",
// e nessuno dei due deve finire interpretato come zero minuti.
func TestRitardoNonNumerico(t *testing.T) {
	b := carica(t, "partenze-1715.html", false)
	for _, tr := range b.Trains {
		if tr.Number == "2975" {
			if tr.Status != "RITARDO" || tr.Delay != 0 || tr.Cancelled {
				t.Errorf("treno 2975: status=%q delay=%d cancelled=%v", tr.Status, tr.Delay, tr.Cancelled)
			}
			return
		}
	}
	t.Skip("treno 2975 non più nel tabellone salvato")
}

// La destinazione è anche l'ultima fermata dell'elenco: è ciò che permette al
// filtro di lavorare solo su Stops, senza trattare la destinazione a parte.
func TestDestinazioneCoincideConUltimaFermata(t *testing.T) {
	b := carica(t, "partenze-1715.html", false)
	var controllati, diversi int
	for _, tr := range b.Trains {
		if len(tr.Stops) == 0 {
			continue
		}
		controllati++
		if ultima := tr.Stops[len(tr.Stops)-1].Name; !strings.HasPrefix(ultima, strings.Split(tr.Terminus, " ")[0]) {
			diversi++
			t.Logf("treno %s: destinazione %q, ultima fermata %q", tr.Number, tr.Terminus, ultima)
		}
	}
	if controllati == 0 {
		t.Fatal("nessun treno con fermate")
	}
	if diversi > controllati/4 {
		t.Errorf("%d treni su %d con ultima fermata diversa dalla destinazione", diversi, controllati)
	}
}

// Sugli arrivi RFI non pubblica le fermate: il filtro per destinazione lì non
// può funzionare, e l'interfaccia deve saperlo.
func TestParseArriviSenzaFermate(t *testing.T) {
	b := carica(t, "arrivi-1715.html", true)
	if len(b.Trains) == 0 {
		t.Fatal("nessun treno")
	}
	for _, tr := range b.Trains {
		if len(tr.Stops) > 0 {
			t.Fatalf("treno %s ha fermate su un tabellone arrivi: %+v", tr.Number, tr.Stops)
		}
	}
}

// Una riga senza contenuto non deve arrivare al client: apparirebbe come una
// scheda vuota in mezzo all'elenco, indistinguibile da un errore.
func TestRigheVuoteScartate(t *testing.T) {
	const pagina = `<html><body>
	<h1 class="nomestazione" id="nomeStazioneId">PROVA</h1>
	<table id="monitor"><tbody>
	  <tr id="123" name="treno">
	    <td id="RTreno">123</td>
	    <td id="RStazione" name="Destinazione"><div>ALTROVE</div></td>
	    <td id="ROrario">10:00</td>
	  </tr>
	  <tr id="" name="treno">
	    <td id="RTreno"></td>
	    <td id="RStazione" name="Destinazione"><div></div></td>
	    <td id="ROrario"></td>
	  </tr>
	  <tr id="" name="treno"></tr>
	</tbody></table></body></html>`

	b, err := Parse(strings.NewReader(pagina), 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Trains) != 1 {
		t.Fatalf("%d treni, atteso 1: %+v", len(b.Trains), b.Trains)
	}
	if b.Trains[0].Terminus != "ALTROVE" || b.Trains[0].Time != "10:00" {
		t.Errorf("treno = %+v", b.Trains[0])
	}
}
