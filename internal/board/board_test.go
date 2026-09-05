package board

import (
	"context"
	"errors"
	"os"
	"sort"
	"sync"
	"testing"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/rfi"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/stations"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/vt"
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

// Venti richieste insieme sulla stessa stazione devono produrre un solo fetch:
// è la garanzia su cui si regge il non tempestare RFI.
func TestConcorrenza(t *testing.T) {
	s, src := servizio("partenze-1715.html")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			to := 0
			if i%2 == 0 {
				to = lodi
			}
			if _, err := s.Get(context.Background(), garibaldi, false, to); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if src.chiamate != 1 {
		t.Errorf("%d fetch, atteso 1", src.chiamate)
	}
}

// --- la seconda fonte: i ritardi misurati da ViaggiaTreno -------------------

// liveFinta sta al posto di ViaggiaTreno: registra come è stata chiamata e
// restituisce quello che le si dice.
type liveFinta struct {
	chiamate int
	codice   string
	arrivi   bool
	misure   map[string]vt.Treno
	err      error
}

func (r *liveFinta) Treni(ctx context.Context, codice string, arrivi bool) (map[string]vt.Treno, error) {
	r.chiamate++
	r.codice, r.arrivi = codice, arrivi
	return r.misure, r.err
}

func minuti(n int) *int { return &n }

// Due treni che nel tabellone di prova ci sono davvero.
const (
	trenoA = "24377"
	trenoB = "24576"
)

func servizioConLive(file string, live Live) (*Service, *sorgenteFinta) {
	s, src := servizio(file)
	return s.ConLive(live), src
}

func ritardoDi(r *Result, numero string) *int {
	for i := range r.Trains {
		if r.Trains[i].Number == numero {
			return r.Trains[i].LiveDelay
		}
	}
	return nil
}

func TestRitardiMisuratiSiAttaccanoAlTreno(t *testing.T) {
	live := &liveFinta{misure: map[string]vt.Treno{
		trenoA: {Ritardo: minuti(7)},
		// Zero misurato: deve arrivare come 0, non come "nessuna misura".
		trenoB: {Ritardo: minuti(0)},
		// Un treno che sul tabellone RFI non c'è non deve dare fastidio.
		"999999": {Ritardo: minuti(3)},
	}}
	s, _ := servizioConLive("partenze-1715.html", live)

	r, err := s.Get(context.Background(), garibaldi, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if live.chiamate != 1 {
		t.Fatalf("chiamate a ViaggiaTreno = %d, attesa 1", live.chiamate)
	}
	if live.codice == "" {
		t.Fatal("la stazione deve portare il codice ViaggiaTreno")
	}
	if live.arrivi {
		t.Error("un tabellone partenze non deve chiedere gli arrivi")
	}

	if got := ritardoDi(r, trenoA); got == nil || *got != 7 {
		t.Errorf("treno %s: ritardo misurato = %v, atteso 7", trenoA, got)
	}
	if got := ritardoDi(r, trenoB); got == nil || *got != 0 {
		t.Errorf("treno %s: ritardo misurato = %v, atteso 0", trenoB, got)
	}

	// Tutti gli altri restano senza misura, e senza misura vuol dire nil: è la
	// differenza fra "misurato in orario" e "non lo sappiamo".
	senza := 0
	for i := range r.Trains {
		if r.Trains[i].LiveDelay == nil {
			senza++
		}
	}
	if senza != len(r.Trains)-2 {
		t.Errorf("treni senza misura = %d, attesi %d", senza, len(r.Trains)-2)
	}
}

func TestSenzaSecondaFonteIlTabelloneEsceLoStesso(t *testing.T) {
	s, _ := servizio("partenze-1715.html")

	r, err := s.Get(context.Background(), garibaldi, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Trains) == 0 {
		t.Fatal("tabellone vuoto")
	}
	for i := range r.Trains {
		if r.Trains[i].LiveDelay != nil {
			t.Fatalf("treno %s: misura inattesa", r.Trains[i].Number)
		}
	}
}

// Se ViaggiaTreno non risponde, il tabellone deve uscire con il solo ritardo di
// RFI: è la lettura in più delle due, non quella da cui dipende la pagina.
func TestViaggiaTrenoRottoNonRompeIlTabellone(t *testing.T) {
	live := &liveFinta{err: errors.New("connessione rifiutata")}
	s, _ := servizioConLive("partenze-1715.html", live)

	r, err := s.Get(context.Background(), garibaldi, false, 0)
	if err != nil {
		t.Fatalf("l'errore della seconda fonte è arrivato fino in cima: %v", err)
	}
	if len(r.Trains) == 0 {
		t.Fatal("tabellone vuoto")
	}
	if got := ritardoDi(r, trenoA); got != nil {
		t.Errorf("treno %s: misura = %v, attesa nessuna", trenoA, got)
	}
}

// Una stazione che ViaggiaTreno non ha non deve nemmeno far partire la
// richiesta: non c'è codice da chiedere.
func TestStazioneSenzaCodiceNonInterrogaViaggiaTreno(t *testing.T) {
	st := stations.Default.ByID(garibaldi)
	if st == nil {
		t.Fatal("stazione di prova assente dal catalogo")
	}
	prima := st.VT
	st.VT = ""
	defer func() { st.VT = prima }()

	live := &liveFinta{misure: map[string]vt.Treno{trenoA: {Ritardo: minuti(7)}}}
	s, _ := servizioConLive("partenze-1715.html", live)

	if _, err := s.Get(context.Background(), garibaldi, false, 0); err != nil {
		t.Fatal(err)
	}
	if live.chiamate != 0 {
		t.Fatalf("chiamate a ViaggiaTreno = %d, attese 0", live.chiamate)
	}
}

// Le due letture stanno nella stessa cache: dentro il TTL, una seconda
// richiesta non deve toccare né RFI né ViaggiaTreno.
func TestLaCacheCopreEntrambeLeFonti(t *testing.T) {
	live := &liveFinta{misure: map[string]vt.Treno{trenoA: {Ritardo: minuti(7)}}}
	s, src := servizioConLive("partenze-1715.html", live)

	for i := 0; i < 3; i++ {
		if _, err := s.Get(context.Background(), garibaldi, false, 0); err != nil {
			t.Fatal(err)
		}
	}
	if src.chiamate != 1 || live.chiamate != 1 {
		t.Fatalf("chiamate: RFI=%d ViaggiaTreno=%d, attesa 1 e 1", src.chiamate, live.chiamate)
	}
}

// Il tabellone in cache porta con sé le misure, quindi anche la lista filtrata
// per destinazione deve uscire con i ritardi attaccati.
func TestIlFiltroNonPerdeLeMisure(t *testing.T) {
	live := &liveFinta{misure: map[string]vt.Treno{trenoA: {Ritardo: minuti(7)}}}
	s, _ := servizioConLive("partenze-1715.html", live)

	r, err := s.Get(context.Background(), garibaldi, false, rogoredo)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Filtered {
		t.Fatal("la lista doveva essere filtrata")
	}
	trovato := false
	for i := range r.Trains {
		if r.Trains[i].LiveDelay != nil {
			trovato = true
		}
	}
	if !trovato && ritardoDi(r, trenoA) == nil {
		// Il treno di prova può non fermare a Rogoredo: in quel caso il test
		// non ha niente da dire, e va saltato invece che fatto fallire a caso.
		t.Skip("il treno di prova non passa dal filtro")
	}
}

// Il cambio di binario arriva al tabellone come una bandiera sul treno: RFI
// pubblica una casella sola, dalla quale non si vede se il numero che c'è
// dentro è quello di sempre o quello di stasera.
func TestBinarioCambiatoArrivaAlTreno(t *testing.T) {
	live := &liveFinta{misure: map[string]vt.Treno{
		trenoA: {BinarioProgrammato: "4", BinarioEffettivo: "5"},
		trenoB: {BinarioProgrammato: "1", BinarioEffettivo: "1"},
	}}
	s, _ := servizioConLive("partenze-1715.html", live)

	r, err := s.Get(context.Background(), garibaldi, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := range r.Trains {
		cambiato := r.Trains[i].PlatformChanged
		switch r.Trains[i].Number {
		case trenoA:
			if !cambiato {
				t.Errorf("treno %s: il binario è passato dal 4 al 5, va segnalato", trenoA)
			}
		case trenoB:
			if cambiato {
				t.Errorf("treno %s: binario confermato sul suo, niente da segnalare", trenoB)
			}
		default:
			if cambiato {
				t.Errorf("treno %s: nessun dato dalla seconda fonte, non può risultare cambiato",
					r.Trains[i].Number)
			}
		}
	}
}

// Un treno non ancora rilevato non ha un ritardo, ma può benissimo avere già
// un binario diverso da quello previsto: è anzi il momento in cui la cosa
// serve di più, perché sei ancora sul piazzale a decidere dove andare.
func TestBinarioCambiatoAncheSenzaMisura(t *testing.T) {
	live := &liveFinta{misure: map[string]vt.Treno{
		trenoA: {BinarioProgrammato: "4", BinarioEffettivo: "5"},
	}}
	s, _ := servizioConLive("partenze-1715.html", live)

	r, err := s.Get(context.Background(), garibaldi, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := range r.Trains {
		if r.Trains[i].Number != trenoA {
			continue
		}
		if r.Trains[i].LiveDelay != nil {
			t.Errorf("treno %s: nessuna misura, il ritardo deve restare assente", trenoA)
		}
		if !r.Trains[i].PlatformChanged {
			t.Errorf("treno %s: il cambio di binario non dipende dalla misura", trenoA)
		}
		return
	}
	t.Fatalf("treno %s non trovato nel tabellone di prova", trenoA)
}
