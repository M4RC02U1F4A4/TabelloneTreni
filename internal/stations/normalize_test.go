package stations

import "testing"

func TestCanon(t *testing.T) {
	casi := map[string]string{
		"MILANO PORTA GARIBALDI": "MILANO PORTA GARIBALDI",
		"MI.P.GARIBALDI":         "MI P GARIBALDI",
		"MI BOVISA P.":           "MI BOVISA P",
		"CANTU'-CERMENATE":       "CANTU CERMENATE",
		// RFI applica l'escaping più volte, quindi l'apostrofo può arrivare
		// anche come sequenza letterale di entity non sciolte.
		"CANTU&#39;&#39;-CERMENATE": "CANTU 39 39 CERMENATE",
		"CASSANO D`ADDA":            "CASSANO D ADDA",
		"REGGIO NELL'EMILIA":        "REGGIO NELL EMILIA",
		"  spazi   doppi  ":         "SPAZI DOPPI",
		"":                          "",
	}
	for in, want := range casi {
		if got := Canon(in); got != want {
			t.Errorf("Canon(%q) = %q, atteso %q", in, got, want)
		}
	}
}

func TestCombacia(t *testing.T) {
	uguali := [][2]string{
		{"MI BOVISA P", "MILANO BOVISA POLITECNICO"},
		{"GAZZADA SCHIAN M", "GAZZADA SCHIANNO MORAZZONE"},
		{"MI P VENEZIA", "MILANO PORTA VENEZIA"},
		{"S DONATO MILAN", "SAN DONATO MILANESE"},
		{"PIOLTELLO LIM", "PIOLTELLO LIMITO"},
	}
	// Nota: le contrazioni in cui il punto spezza una parola sola — "BOLOGNA
	// C.LE" per CENTRALE, "TORINO P.NUOVA" — non le risolve Combacia, che
	// ragiona per token, ma il livello degli alias. Il caso è coperto in
	// TestMatcherContrazioni.
	for _, c := range uguali {
		if !Combacia(c[0], c[1]) {
			t.Errorf("Combacia(%q, %q) = false, atteso true", c[0], c[1])
		}
	}

	// Il verso pericoloso: un falso positivo qui mostrerebbe un treno che non
	// ferma dove serve.
	diversi := [][2]string{
		{"MILANO CENTRALE", "MILANO CERTOSA"},
		{"MI P VENEZIA", "MILANO PORTA VITTORIA"},
		// Il vincolo sul numero di token è ciò che impedisce a una stazione di
		// assorbire quelle che la contengono come prefisso.
		{"LODI", "LODI VECCHIO"},
		{"RHO", "RHO FIERA"},
		{"MILANO PORTA GARIBALDI", "MILANO PORTA GARIBALDI SOTTERRANEA"},
		{"", "MILANO CENTRALE"},
		{"MI", "MILANO CENTRALE"},
	}
	for _, c := range diversi {
		if Combacia(c[0], c[1]) {
			t.Errorf("Combacia(%q, %q) = true, atteso false", c[0], c[1])
		}
	}
}

// Le contrazioni che Combacia non può sciogliere devono comunque funzionare,
// perché a scioglierle è il nome breve preso da ViaggiaTreno.
func TestMatcherContrazioni(t *testing.T) {
	casi := map[int]string{
		683:  "BOLOGNA C.LE",   // BOLOGNA CENTRALE
		2876: "TORINO P.NUOVA", // TORINO PORTA NUOVA
		1715: "MI.P.GARIBALDI", // MILANO PORTA GARIBALDI
	}
	for id, fermata := range casi {
		st := Default.ByID(id)
		if st == nil {
			t.Errorf("stazione %d assente dal catalogo", id)
			continue
		}
		if !Default.Matcher(id).Matches(fermata) {
			t.Errorf("%q non risolve a %q (alias noti: %v)", fermata, st.Name, st.Aliases)
		}
	}
}

func TestCatalogoDefault(t *testing.T) {
	if len(Default.Elenco) < 2000 {
		t.Fatalf("catalogo con sole %d stazioni: generazione incompleta?", len(Default.Elenco))
	}
	garibaldi := Default.ByID(1715)
	if garibaldi == nil || garibaldi.Name != "MILANO PORTA GARIBALDI" {
		t.Fatalf("stazione 1715 = %+v", garibaldi)
	}
	if !Default.Matcher(1715).Matches("MI.P.GARIBALDI") {
		t.Error(`l'alias "MI.P.GARIBALDI" non risolve a Milano Porta Garibaldi`)
	}
	if Default.Matcher(999999) != nil {
		t.Error("Matcher su una stazione inesistente dovrebbe dare nil")
	}
}

// Una stazione con il piano sotterraneo porta con sé anche il codice
// ViaggiaTreno di quel piano: il tabellone RFI è uno solo per tutti e due,
// mentre ViaggiaTreno li tiene separati.
func TestLivelliCollegati(t *testing.T) {
	const (
		portaGaribaldi            = 1715
		portaGaribaldiSotterranea = 1714
	)
	sopra := Default.ByID(portaGaribaldi)
	sotto := Default.ByID(portaGaribaldiSotterranea)
	if sopra == nil || sotto == nil {
		t.Fatal("stazioni di prova assenti dal catalogo")
	}

	codici := sopra.CodiciVT()
	if len(codici) != 2 || codici[0] != sopra.VT || codici[1] != sotto.VT {
		t.Fatalf("codici = %v, attesi [%s %s]", codici, sopra.VT, sotto.VT)
	}
	// Il piano di sotto non tira dentro quello di sopra: il suo tabellone RFI
	// porta i soli treni sotterranei.
	if got := sotto.CodiciVT(); len(got) != 1 {
		t.Errorf("il piano inferiore ha codici %v, atteso solo il suo", got)
	}
}

// Il collegamento guarda il nome esatto più "SOTTERRANEA", non un nome che
// comincia per un altro: ALBA e ALBA ADRIATICA sono due paesi diversi, e
// unirle vorrebbe dire mescolare i treni di due linee.
func TestNomiCheSiSomiglianoNonSonoLivelli(t *testing.T) {
	for _, nome := range []string{"ALBA", "ANCONA", "BERGAMO", "MILANO CENTRALE"} {
		var s *Station
		for _, cand := range Default.Elenco {
			if cand.Name == nome {
				s = cand
				break
			}
		}
		if s == nil {
			t.Fatalf("%s non è nel catalogo", nome)
		}
		if len(s.VTAlt) != 0 {
			t.Errorf("%s: livelli collegati %v, atteso nessuno", nome, s.VTAlt)
		}
	}
}
