package board

import (
	"context"
	"os"
	"sort"
	"testing"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/rfi"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/stations"
)

// sorgenteFinta serve il tabellone salvato in testdata di internal/rfi, così i
// test sul filtro girano offline e su dati veri.
type sorgenteFinta struct {
	chiamate int
	file     string
}

func (s *sorgenteFinta) Fetch(ctx context.Context, placeID int, arrivals bool) (*rfi.Board, error) {
	s.chiamate++
	f, err := os.Open("../rfi/testdata/" + s.file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return rfi.Parse(f, placeID, arrivals)
}

const (
	garibaldi = 1715
	lodi      = 1584
	pavia     = 2046
	rogoredo  = 1720
	varese    = 2994
)

func servizio(file string) (*Service, *sorgenteFinta) {
	src := &sorgenteFinta{file: file}
	return New(src, stations.Default), src
}

func TestFiltroPerDestinazione(t *testing.T) {
	s, _ := servizio("partenze-1715.html")

	tutti, err := s.Get(context.Background(), garibaldi, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if tutti.Total != 40 || tutti.Filtered {
		t.Fatalf("tabellone intero: total=%d filtered=%v", tutti.Total, tutti.Filtered)
	}

	casi := []struct {
		nome    string
		to      int
		almeno  int
		alMassi int
	}{
		// Lodi e Pavia stanno su due linee diverse: se il filtro fosse rotto
		// nel verso di lasciar passare tutto, questi due numeri sarebbero uguali.
		{"LODI", lodi, 1, 20},
		{"PAVIA", pavia, 1, 20},
		// Rogoredo è servita da entrambe, quindi ne deve avere più di ciascuna.
		{"MILANO ROGOREDO", rogoredo, 2, 30},
	}
	for _, c := range casi {
		r, err := s.Get(context.Background(), garibaldi, false, c.to)
		if err != nil {
			t.Fatalf("%s: %v", c.nome, err)
		}
		if !r.Filtered {
			t.Errorf("%s: risultato non filtrato", c.nome)
		}
		n := len(r.Trains)
		if n < c.almeno || n > c.alMassi {
			t.Errorf("%s: %d treni, attesi fra %d e %d", c.nome, n, c.almeno, c.alMassi)
		}
		for _, tr := range r.Trains {
			if tr.Arrival == "" {
				t.Errorf("%s: treno %s senza orario di arrivo", c.nome, tr.Number)
			}
		}
	}
}

// Il filtro deve riconoscere la stazione anche quando il tabellone la scrive
// abbreviata: è il caso che rende il filtro utile o inutile.
func TestFiltroRiconosceAbbreviazioni(t *testing.T) {
	s, _ := servizio("partenze-1715.html")
	r, err := s.Get(context.Background(), garibaldi, false, rogoredo)
	if err != nil {
		t.Fatal(err)
	}
	// Nel tabellone Rogoredo compare per esteso, ma i treni per Lodi passano
	// anche da "S.DONATO MILAN." e "MI. P. VENEZIA": verifichiamo che almeno
	// una forma abbreviata venga risolta, su Milano Porta Venezia.
	rv, err := s.Get(context.Background(), garibaldi, false, 1723) // MILANO PORTA VENEZIA
	if err != nil {
		t.Fatal(err)
	}
	if len(rv.Trains) == 0 {
		t.Error(`nessun treno per MILANO PORTA VENEZIA: la forma "MI. P. VENEZIA" non viene riconosciuta`)
	}
	if len(r.Trains) == 0 {
		t.Error("nessun treno per MILANO ROGOREDO")
	}
}

// Una stazione che non è servita da questo tabellone non deve produrre falsi
// positivi: è il verso di errore che renderebbe il filtro dannoso.
func TestFiltroSenzaCorrispondenze(t *testing.T) {
	s, _ := servizio("partenze-1715.html")
	r, err := s.Get(context.Background(), garibaldi, false, varese)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range r.Trains {
		var fermate []string
		for _, f := range tr.Stops {
			fermate = append(fermate, f.Name)
		}
		t.Logf("treno %s per %s ferma a %v", tr.Number, tr.Terminus, fermate)
	}
	if len(r.Trains) > 3 {
		t.Errorf("%d treni per VARESE da Garibaldi: troppi, il filtro è troppo permissivo", len(r.Trains))
	}
}

func TestArriviNonFiltrabili(t *testing.T) {
	s, _ := servizio("arrivi-1715.html")
	r, err := s.Get(context.Background(), garibaldi, true, lodi)
	if err != nil {
		t.Fatal(err)
	}
	if !r.StopsUnavailable {
		t.Error("il risultato non segnala che le fermate mancano")
	}
	if r.Filtered {
		t.Error("gli arrivi non possono essere filtrati")
	}
	if len(r.Trains) == 0 {
		t.Error("filtrando gli arrivi si è svuotato il tabellone")
	}
}

// La cache è ciò che tiene una sola richiesta a RFI anche con molti client:
// se smette di funzionare, il carico verso RFI si moltiplica in silenzio.
func TestCacheUnaSolaRichiesta(t *testing.T) {
	s, src := servizio("partenze-1715.html")
	for i := 0; i < 5; i++ {
		if _, err := s.Get(context.Background(), garibaldi, false, lodi); err != nil {
			t.Fatal(err)
		}
	}
	if src.chiamate != 1 {
		t.Errorf("%d richieste alla sorgente, attesa 1", src.chiamate)
	}
}

// fuoriCatalogo sono le fermate che compaiono sul tabellone ma che il catalogo
// RFI non contiene affatto: la rete svizzera oltre Chiasso, le stazioni
// Ferrovienord, e qualche impianto (Malpensa, Bovisa) che RFI serve senza
// elencarlo fra le località selezionabili. Non sono errori di riconoscimento —
// non c'è niente da riconoscere — e non possono nemmeno essere scelte come
// destinazione, quindi il filtro non ne risente.
var fuoriCatalogo = map[string]bool{
	"BALERNA": true, "BELLINZONA": true, "CAPOLAGO R.S.V.": true, "GIUBIASCO": true,
	"LAMONE-CADEMPINO": true, "LUGANO": true, "LUGANO PARADISO": true, "MAROGGIA-MELANO": true,
	"MELIDE": true, "MENDRISIO": true, "MENDRISIO S.M.": true, "MEZZOVICO": true,
	"RIVERA-B.": true, "TAVERNE-TOR.": true,
	"BUSTO ARSIZIO FN": true, "CASTELLANZA": true, "RESCALDINA": true, "SARONNO": true,
	"FERNO-LONATE P.": true, "MALPENSA AEROPORTO": true, "MI BOVISA P.": true,
}

// Ogni fermata che corrisponde a una stazione esistente deve essere
// riconosciuta: è il presupposto del filtro. Le eccezioni stanno tutte in
// fuoriCatalogo, esplicitamente, così una nuova fermata non riconosciuta fa
// fallire il test invece di sparire in una percentuale.
func TestCoperturaNomiFermate(t *testing.T) {
	s, _ := servizio("partenze-1715.html")
	b, err := s.Get(context.Background(), garibaldi, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	nomi := map[string]bool{}
	for _, tr := range b.Trains {
		for _, f := range tr.Stops {
			nomi[f.Name] = true
		}
	}
	if len(nomi) < 50 {
		t.Fatalf("solo %d fermate distinte: il tabellone salvato non è rappresentativo", len(nomi))
	}

	var mancanti, inattesi []string
	for nome := range nomi {
		trovato := false
		for _, st := range stations.Default.Elenco {
			if stations.Default.Matcher(st.ID).Matches(nome) {
				trovato = true
				break
			}
		}
		switch {
		case !trovato && !fuoriCatalogo[nome]:
			mancanti = append(mancanti, nome)
		case trovato && fuoriCatalogo[nome]:
			inattesi = append(inattesi, nome)
		}
	}
	sort.Strings(mancanti)
	if len(mancanti) > 0 {
		t.Errorf("fermate non ricondotte ad alcuna stazione: %v\n"+
			"se esistono davvero nel catalogo, aggiungere un alias in cmd/genstations", mancanti)
	}
	// Se una di queste comincia a risolversi, o il catalogo è cresciuto o il
	// riconoscimento è diventato troppo permissivo: in entrambi i casi va vista.
	if len(inattesi) > 0 {
		t.Errorf("fermate date per fuori catalogo ma riconosciute: %v", inattesi)
	}
}
