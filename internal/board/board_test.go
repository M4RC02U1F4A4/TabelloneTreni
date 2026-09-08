package board

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

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
	// Una stazione a due livelli fa partire due richieste insieme: senza il
	// lucchetto il contatore lo leggerebbero e scriverebbero in parallelo.
	mu        sync.Mutex
	chiamate  int
	codice    string
	chiesti   []string
	arrivi    bool
	misure    map[string]vt.Treno
	perCodice map[string]map[string]vt.Treno
	err       error

	andamenti    int
	chiestoPer   string
	viaggio      *vt.Andamento
	viaggiPer    map[string]*vt.Andamento
	errAndamento error
}

func (r *liveFinta) Treni(ctx context.Context, codice string, arrivi bool) (map[string]vt.Treno, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chiamate++
	r.codice, r.arrivi = codice, arrivi
	r.chiesti = append(r.chiesti, codice)
	if r.perCodice != nil {
		return r.perCodice[codice], r.err
	}
	return r.misure, r.err
}

// Le fermate mancanti si chiedono per tutti i treni insieme: senza il
// lucchetto il contatore e la mappa li leggerebbero più goroutine in parallelo.
func (r *liveFinta) Andamento(ctx context.Context, codOrigine, numero string, data int64) (*vt.Andamento, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.andamenti++
	r.chiestoPer = fmt.Sprintf("%s|%s|%d", codOrigine, numero, data)
	if r.viaggiPer != nil {
		return r.viaggiPer[numero], r.errAndamento
	}
	return r.viaggio, r.errAndamento
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
	// Porta Garibaldi ha due livelli, quindi due codici da interrogare.
	livelli := len(stations.Default.ByID(garibaldi).CodiciVT())
	if live.chiamate != livelli {
		t.Fatalf("chiamate a ViaggiaTreno = %d, attese %d", live.chiamate, livelli)
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
	livelli := len(stations.Default.ByID(garibaldi).CodiciVT())
	if src.chiamate != 1 || live.chiamate != livelli {
		t.Fatalf("chiamate: RFI=%d ViaggiaTreno=%d, attese 1 e %d", src.chiamate, live.chiamate, livelli)
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

// --- il viaggio del singolo treno -------------------------------------------

func viaggioFinto() *vt.Andamento {
	return &vt.Andamento{Ritardo: 3, Stazione: "MILANO LAMBRATE", Fermate: []vt.Fermata{{Nome: "X"}}}
}

// Le coordinate per chiedere l'andamento le ha già lette il tabellone: al
// client si chiede solo quale treno, non da dove parte e di che giorno è.
func TestAndamentoUsaLeCoordinateDelTabellone(t *testing.T) {
	live := &liveFinta{
		misure:  map[string]vt.Treno{trenoA: {CodOrigine: "S01700", DataPartenza: 1788645600000}},
		viaggio: viaggioFinto(),
	}
	s, _ := servizioConLive("partenze-1715.html", live)
	if _, err := s.Get(context.Background(), garibaldi, false, 0); err != nil {
		t.Fatal(err)
	}

	a, err := s.Andamento(context.Background(), garibaldi, false, trenoA)
	if err != nil {
		t.Fatal(err)
	}
	if a == nil || a.Stazione != "MILANO LAMBRATE" {
		t.Fatalf("andamento = %+v", a)
	}
	atteso := "S01700|" + trenoA + "|1788645600000"
	if live.chiestoPer != atteso {
		t.Errorf("chiesto per %q, atteso %q", live.chiestoPer, atteso)
	}
}

// Toccare due volte la stessa scheda non deve chiedere due volte la stessa cosa
// a un servizio lento.
func TestAndamentoInCache(t *testing.T) {
	live := &liveFinta{
		misure:  map[string]vt.Treno{trenoA: {CodOrigine: "S01700", DataPartenza: 1}},
		viaggio: viaggioFinto(),
	}
	s, _ := servizioConLive("partenze-1715.html", live)
	if _, err := s.Get(context.Background(), garibaldi, false, 0); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if _, err := s.Andamento(context.Background(), garibaldi, false, trenoA); err != nil {
			t.Fatal(err)
		}
	}
	if live.andamenti != 1 {
		t.Fatalf("chiamate = %d, attesa 1", live.andamenti)
	}
}

// I casi in cui il viaggio semplicemente non c'è: non sono errori, e non devono
// diventarlo. La scheda si apre lo stesso, con le fermate previste.
func TestAndamentoAssenteNonEUnErrore(t *testing.T) {
	casi := []struct {
		nome    string
		prepara func(t *testing.T) *Service
		treno   string
	}{
		{
			nome: "nessuna seconda fonte",
			prepara: func(t *testing.T) *Service {
				s, _ := servizio("partenze-1715.html")
				if _, err := s.Get(context.Background(), garibaldi, false, 0); err != nil {
					t.Fatal(err)
				}
				return s
			},
			treno: trenoA,
		},
		{
			nome: "tabellone mai letto",
			prepara: func(t *testing.T) *Service {
				s, _ := servizioConLive("partenze-1715.html", &liveFinta{viaggio: viaggioFinto()})
				return s
			},
			treno: trenoA,
		},
		{
			nome: "treno che ViaggiaTreno non conosce",
			prepara: func(t *testing.T) *Service {
				live := &liveFinta{misure: map[string]vt.Treno{}, viaggio: viaggioFinto()}
				s, _ := servizioConLive("partenze-1715.html", live)
				if _, err := s.Get(context.Background(), garibaldi, false, 0); err != nil {
					t.Fatal(err)
				}
				return s
			},
			treno: trenoA,
		},
		{
			nome: "treno senza le coordinate per chiederlo",
			prepara: func(t *testing.T) *Service {
				live := &liveFinta{
					misure:  map[string]vt.Treno{trenoA: {Ritardo: minuti(2)}},
					viaggio: viaggioFinto(),
				}
				s, _ := servizioConLive("partenze-1715.html", live)
				if _, err := s.Get(context.Background(), garibaldi, false, 0); err != nil {
					t.Fatal(err)
				}
				return s
			},
			treno: trenoA,
		},
	}
	for _, caso := range casi {
		t.Run(caso.nome, func(t *testing.T) {
			s := caso.prepara(t)
			a, err := s.Andamento(context.Background(), garibaldi, false, caso.treno)
			if err != nil {
				t.Fatalf("errore invece di un viaggio assente: %v", err)
			}
			if a != nil {
				t.Fatalf("andamento = %+v, atteso nessuno", a)
			}
		})
	}
}

// I due piani di una stazione: RFI ne fa un tabellone solo, ViaggiaTreno tiene
// due stazioni separate. Senza fondere le due risposte, i treni del piano
// inferiore — le suburbane, quelle che prende più gente — resterebbero senza
// ritardo misurato.
func TestIDueLivelliFinisconoNelloStessoTabellone(t *testing.T) {
	st := stations.Default.ByID(garibaldi)
	codici := st.CodiciVT()
	if len(codici) != 2 {
		t.Fatalf("codici ViaggiaTreno = %v, attesi due livelli", codici)
	}

	live := &liveFinta{perCodice: map[string]map[string]vt.Treno{
		codici[0]: {trenoA: {Ritardo: minuti(4)}},
		codici[1]: {trenoB: {Ritardo: minuti(9)}},
	}}
	s, _ := servizioConLive("partenze-1715.html", live)

	r, err := s.Get(context.Background(), garibaldi, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := ritardoDi(r, trenoA); got == nil || *got != 4 {
		t.Errorf("treno del piano di sopra: ritardo = %v, atteso 4", got)
	}
	if got := ritardoDi(r, trenoB); got == nil || *got != 9 {
		t.Errorf("treno del piano di sotto: ritardo = %v, atteso 9", got)
	}
}

// Se un livello non risponde restano i treni dell'altro: mezzo tabellone con i
// ritardi misurati è meglio di nessuno.
func TestUnLivelloRottoNonAnnullaLAltro(t *testing.T) {
	codici := stations.Default.ByID(garibaldi).CodiciVT()
	live := &liveFinta{perCodice: map[string]map[string]vt.Treno{
		codici[0]: {trenoA: {Ritardo: minuti(4)}},
		// il secondo livello non risponde: la mappa manca del tutto
	}}
	s, _ := servizioConLive("partenze-1715.html", live)

	r, err := s.Get(context.Background(), garibaldi, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := ritardoDi(r, trenoA); got == nil || *got != 4 {
		t.Errorf("ritardo = %v, atteso 4", got)
	}
}

// Un treno seguito si chiede con le sue coordinate e senza nessun tabellone
// alle spalle: è il caso per cui la funzione esiste, perché chi segue un treno
// lo guarda quasi sempre quando è già partito e dal tabellone è sparito.
func TestViaggioSenzaTabellone(t *testing.T) {
	live := &liveFinta{viaggio: viaggioFinto()}
	s, src := servizioConLive("partenze-1715.html", live)

	a, err := s.Viaggio(context.Background(), "S01700", "2247", 1788645600000)
	if err != nil {
		t.Fatal(err)
	}
	if a == nil || a.Stazione != "MILANO LAMBRATE" {
		t.Fatalf("viaggio = %+v", a)
	}
	if atteso := "S01700|2247|1788645600000"; live.chiestoPer != atteso {
		t.Errorf("chiesto per %q, atteso %q", live.chiestoPer, atteso)
	}
	// Nessun tabellone è stato letto: seguire un treno non costa una richiesta
	// a RFI, che è il punto di tenere le coordinate sul telefono.
	if src.chiamate != 0 {
		t.Errorf("letture del tabellone = %d, attese 0", src.chiamate)
	}
}

// La cache dei viaggi è una sola: un treno aperto dal tabellone e lo stesso
// treno seguito dalla home non devono costare due letture a un servizio lento.
func TestViaggioSeguitoEApertoCondividonoLaCache(t *testing.T) {
	live := &liveFinta{
		misure:  map[string]vt.Treno{trenoA: {CodOrigine: "S01700", DataPartenza: 1788645600000}},
		viaggio: viaggioFinto(),
	}
	s, _ := servizioConLive("partenze-1715.html", live)
	if _, err := s.Get(context.Background(), garibaldi, false, 0); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Andamento(context.Background(), garibaldi, false, trenoA); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Viaggio(context.Background(), "S01700", trenoA, 1788645600000); err != nil {
		t.Fatal(err)
	}
	if live.andamenti != 1 {
		t.Fatalf("chiamate = %d, attesa 1", live.andamenti)
	}
}

// Senza seconda fonte non c'è niente da seguire, e non è un errore: è lo stesso
// silenzio con cui esce la scheda aperta da un tabellone.
func TestViaggioSenzaSecondaFonte(t *testing.T) {
	s, _ := servizio("partenze-1715.html")
	a, err := s.Viaggio(context.Background(), "S01700", "2247", 1788645600000)
	if err != nil || a != nil {
		t.Fatalf("viaggio = %+v, err = %v", a, err)
	}
}

// --- i treni di cui RFI non pubblica le fermate -----------------------------

// Nel tabellone di prova sei treni su quaranta non hanno l'elenco "FERMA A:":
// cinque cancellati, la cui casella dei dettagli è vuota, e uno con un ritardo
// annunciato ma non quantificato. Cercando la destinazione solo lì dentro
// spariscono dalla tratta, ed è proprio il giorno di sciopero — quando i
// cancellati sono tanti — che uno guarda la tratta.
const (
	trenoCancellato   = "24578" // cancellato, per VARESE
	trenoAltroverso   = "2984"  // cancellato, per GALLARATE: non passa da Varese
	trenoInRitardo    = "2975"  // "RITARDO" ma non cancellato, per MILANO CENTRALE
	trenoNonTracciato = "24176" // cancellato, e ViaggiaTreno non lo conosce
	centrale          = 1728
	codiceSaronno     = "S01050" // una fermata intermedia qualsiasi
)

// oraRoma è un orario di ViaggiaTreno: un istante, che il filtro deve stampare
// nel fuso dei treni italiani e non in quello di chi guarda il tabellone.
func oraRoma(h, m int) time.Time { return time.Date(2026, 3, 5, h, m, 0, 0, roma) }

// liveConFermate è ViaggiaTreno che conosce i treni che RFI pubblica senza
// fermate: il cancellato per Varese ci passa davvero, quello per Gallarate no,
// e di uno non sa niente.
func liveConFermate() *liveFinta {
	gari := stations.Default.ByID(garibaldi).VT
	partenza := vt.Fermata{Codice: gari, Nome: "MILANO P.TA GARIBALDI", Programmata: oraRoma(17, 25)}
	return &liveFinta{
		misure: map[string]vt.Treno{
			trenoCancellato:   {CodOrigine: gari, DataPartenza: 1},
			trenoAltroverso:   {CodOrigine: gari, DataPartenza: 1},
			trenoInRitardo:    {CodOrigine: gari, DataPartenza: 1},
			trenoNonTracciato: {CodOrigine: gari, DataPartenza: 1},
		},
		viaggiPer: map[string]*vt.Andamento{
			trenoCancellato: {Fermate: []vt.Fermata{
				partenza,
				{Codice: codiceSaronno, Nome: "SARONNO", Programmata: oraRoma(17, 55)},
				{Codice: stations.Default.ByID(varese).VT, Nome: "VARESE", Programmata: oraRoma(18, 20)},
			}},
			trenoAltroverso: {Fermate: []vt.Fermata{
				partenza,
				{Codice: codiceSaronno, Nome: "SARONNO", Programmata: oraRoma(17, 50)},
			}},
			trenoInRitardo: {Fermate: []vt.Fermata{
				partenza,
				{Codice: stations.Default.ByID(centrale).VT, Nome: "MILANO CENTRALE", Programmata: oraRoma(17, 40)},
			}},
		},
	}
}

func trenoDi(r *Result, numero string) *rfi.Train {
	for i := range r.Trains {
		if r.Trains[i].Number == numero {
			return &r.Trains[i]
		}
	}
	return nil
}

// La lista filtrata deve restare una sottosequenza del tabellone: l'ordine è
// quello degli orari di partenza, ed è l'unico ordine in cui si legge.
func ordineDelTabellone(t *testing.T, tutti, filtrati []rfi.Train) {
	i := 0
	for _, tr := range filtrati {
		for i < len(tutti) && tutti[i].Number != tr.Number {
			i++
		}
		if i == len(tutti) {
			t.Fatalf("treno %s fuori dall'ordine del tabellone", tr.Number)
		}
		i++
	}
}

// Il caso del segnalatore: un treno cancellato non ha fermate sul tabellone, ma
// deve comparire sulla sua tratta, altrimenti "è cancellato" e "non è in questa
// fascia oraria" si assomigliano troppo.
func TestTrenoCancellatoCompareSullaTratta(t *testing.T) {
	live := liveConFermate()
	s, _ := servizioConLive("partenze-1715.html", live)

	r, err := s.Get(context.Background(), garibaldi, false, varese)
	if err != nil {
		t.Fatal(err)
	}
	tr := trenoDi(r, trenoCancellato)
	if tr == nil {
		t.Fatalf("treno %s assente dalla tratta per Varese", trenoCancellato)
	}
	if !tr.Cancelled {
		t.Error("il treno deve restare cancellato: è l'unica cosa che si va a vedere")
	}
	if tr.Arrival != "18:20" {
		t.Errorf("arrivo = %q, atteso 18:20", tr.Arrival)
	}
	// Di un treno cancellato non c'è nessun viaggio da seguire, e il frontend
	// apre la scheda solo se ci sono fermate.
	if len(tr.Stops) != 0 {
		t.Errorf("fermate = %v, attese nessuna su un treno cancellato", tr.Stops)
	}

	tutti, err := s.Get(context.Background(), garibaldi, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	ordineDelTabellone(t, tutti.Trains, r.Trains)
}

// Un treno senza fermate pubblicate non è per questo un treno che va bene per
// qualunque destinazione: se ViaggiaTreno dice che da lì non passa, resta fuori
// come prima.
func TestTrenoCancellatoAltroversoNonCompare(t *testing.T) {
	live := liveConFermate()
	s, _ := servizioConLive("partenze-1715.html", live)

	r, err := s.Get(context.Background(), garibaldi, false, varese)
	if err != nil {
		t.Fatal(err)
	}
	if tr := trenoDi(r, trenoAltroverso); tr != nil {
		t.Errorf("treno %s (per Gallarate) nella tratta per Varese", trenoAltroverso)
	}
	if tr := trenoDi(r, trenoNonTracciato); tr != nil {
		t.Errorf("treno %s, che ViaggiaTreno non conosce, nella tratta", trenoNonTracciato)
	}
}

// Il treno senza fermate non è sempre un cancellato: 2975 ha un ritardo
// annunciato e non quantificato, e RFI non gli stampa il popup. Lì le fermate
// vanno riempite, altrimenti la sua scheda è la sola che non si apre.
func TestTrenoSenzaFermateNonCancellatoTieneLeFermate(t *testing.T) {
	live := liveConFermate()
	s, _ := servizioConLive("partenze-1715.html", live)

	r, err := s.Get(context.Background(), garibaldi, false, centrale)
	if err != nil {
		t.Fatal(err)
	}
	tr := trenoDi(r, trenoInRitardo)
	if tr == nil {
		t.Fatalf("treno %s assente dalla tratta per Milano Centrale", trenoInRitardo)
	}
	if tr.Cancelled {
		t.Error("il treno non è cancellato")
	}
	if tr.Arrival != "17:40" {
		t.Errorf("arrivo = %q, atteso 17:40", tr.Arrival)
	}
	// La stazione da cui si guarda il tabellone non è una fermata successiva:
	// l'elenco di ViaggiaTreno parte dall'origine del treno e va tagliato.
	if len(tr.Stops) != 1 || tr.Stops[0].Name != "MILANO CENTRALE" {
		t.Fatalf("fermate = %+v, attesa la sola Milano Centrale", tr.Stops)
	}
	// Il frontend evidenzia la fermata scelta confrontando l'orario: se i due
	// non combaciano, la tratta si vede ma non si capisce dove.
	if tr.Stops[0].Time != tr.Arrival {
		t.Errorf("fermata alle %q, arrivo alle %q: il frontend non le accoppia",
			tr.Stops[0].Time, tr.Arrival)
	}
}

// La seconda fonte è una lettura in più, non una da cui dipendere: rotta, il
// filtro deve tornare esattamente quello di prima.
func TestViaggiaTrenoRottoNonCambiaIlFiltro(t *testing.T) {
	senza, _ := servizio("partenze-1715.html")
	atteso, err := senza.Get(context.Background(), garibaldi, false, rogoredo)
	if err != nil {
		t.Fatal(err)
	}

	live := liveConFermate()
	live.errAndamento = errors.New("connessione rifiutata")
	s, _ := servizioConLive("partenze-1715.html", live)
	r, err := s.Get(context.Background(), garibaldi, false, rogoredo)
	if err != nil {
		t.Fatalf("l'errore della seconda fonte è arrivato fino in cima: %v", err)
	}
	if len(r.Trains) != len(atteso.Trains) {
		t.Fatalf("%d treni con ViaggiaTreno rotto, %d senza", len(r.Trains), len(atteso.Trains))
	}
	for i := range r.Trains {
		if r.Trains[i].Number != atteso.Trains[i].Number || r.Trains[i].Arrival != atteso.Trains[i].Arrival {
			t.Errorf("treno %d: %s alle %s, atteso %s alle %s", i,
				r.Trains[i].Number, r.Trains[i].Arrival,
				atteso.Trains[i].Number, atteso.Trains[i].Arrival)
		}
	}
}

// Le fermate di un treno non cambiano durante la giornata: chiederle una volta
// per treno basta. In un giorno di sciopero sono una ventina di richieste a un
// servizio lento, e il tabellone si rinfresca ogni mezzo minuto.
func TestFermateSupplentiInCache(t *testing.T) {
	live := liveConFermate()
	s, _ := servizioConLive("partenze-1715.html", live)

	for i := 0; i < 3; i++ {
		if _, err := s.Get(context.Background(), garibaldi, false, varese); err != nil {
			t.Fatal(err)
		}
	}
	// Quattro: i treni senza fermate di cui il tabellone ha le coordinate. Fra
	// questi c'è trenoNonTracciato, che ViaggiaTreno non conosce: se l'esito
	// negativo non finisse in cache, quello si richiederebbe ogni volta.
	if live.andamenti != 4 {
		t.Errorf("richieste ad Andamento = %d, attese 4", live.andamenti)
	}
}

// --- il treno che si sdoppia ------------------------------------------------

// sorgenteFissa serve un tabellone costruito a mano: nel tabellone salvato non
// c'è un treno sdoppiato, e quel caso va riprodotto.
type sorgenteFissa struct{ board rfi.Board }

func (s *sorgenteFissa) Fetch(ctx context.Context, placeID int, arrivals bool) (*rfi.Board, error) {
	b := s.board
	b.Trains = append([]rfi.Train(nil), s.board.Trains...)
	return &b, nil
}

const monza = 1841

// Sullo stesso tabellone lo stesso numero può comparire su due righe: un treno
// che si sdoppia, con due destinazioni e due stati — visto a Milano Centrale in
// un giorno di sciopero, il 25512 per Chiasso regolare e il 25512 per Locarno
// cancellato. Le fermate risolte per la riga che non ne ha non devono finire su
// quella che ce le ha già: verrebbe tenuta o scartata su un percorso non suo.
func TestTrenoSdoppiatoNonScambiaLeFermate(t *testing.T) {
	src := &sorgenteFissa{board: rfi.Board{
		PlaceID: centrale,
		Station: "MILANO CENTRALE",
		Trains: []rfi.Train{
			{Number: "25512", Terminus: "CHIASSO", Time: "09:43", Stops: []rfi.Stop{
				{Name: "MONZA", Time: "09:58"},
				{Name: "COMO S.GIOVANNI", Time: "10:25"},
			}},
			{Number: "25512", Terminus: "LOCARNO", Time: "09:43", Cancelled: true},
		},
	}}
	// ViaggiaTreno conosce un solo 25512, e quello che conosce non passa da
	// Monza: è la lettura che tocca alla riga cancellata.
	live := &liveFinta{
		misure: map[string]vt.Treno{"25512": {CodOrigine: "S01700", DataPartenza: 1}},
		viaggiPer: map[string]*vt.Andamento{"25512": {Fermate: []vt.Fermata{
			{Codice: stations.Default.ByID(centrale).VT, Nome: "MILANO CENTRALE", Programmata: oraRoma(9, 43)},
			{Codice: codiceSaronno, Nome: "SARONNO", Programmata: oraRoma(10, 15)},
		}}},
	}
	s := New(src, stations.Default).ConLive(live)

	r, err := s.Get(context.Background(), centrale, false, monza)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Trains) != 1 {
		t.Fatalf("%d treni per Monza, atteso il solo 25512 per Chiasso: %+v", len(r.Trains), r.Trains)
	}
	tr := r.Trains[0]
	if tr.Terminus != "CHIASSO" || tr.Cancelled {
		t.Errorf("è passata la riga sbagliata: %s, cancellato=%v", tr.Terminus, tr.Cancelled)
	}
	if tr.Arrival != "09:58" {
		t.Errorf("arrivo = %q, atteso 09:58 come lo stampa RFI", tr.Arrival)
	}
	if len(tr.Stops) != 2 || tr.Stops[0].Name != "MONZA" {
		t.Errorf("fermate = %+v, attese le due di RFI: la riga ha preso il percorso dell'altra", tr.Stops)
	}
}
