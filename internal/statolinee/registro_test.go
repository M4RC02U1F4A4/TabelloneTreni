package statolinee

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/trenord"
)

func linee(coppie ...any) []trenord.Linea {
	var out []trenord.Linea
	for i := 0; i < len(coppie); i += 2 {
		out = append(out, trenord.Linea{
			Codice: coppie[i].(string),
			Nome:   coppie[i].(string) + " prova",
			Stato:  coppie[i+1].(trenord.Stato),
		})
	}
	return out
}

var quando = time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)

// Il primo giro non è mai una notizia: senza questa regola ogni riavvio del
// servizio manderebbe una notifica per ogni linea che in quel momento non è
// regolare — e i riavvii sono uno per rilascio.
func TestPrimoGiroNonProduceCambi(t *testing.T) {
	r := NuovoRegistro()
	cambi := r.Aggiorna(linee("S1", trenord.Regolare, "S2", trenord.Critico), quando)
	if len(cambi) != 0 {
		t.Fatalf("cambi = %+v, atteso nessuno", cambi)
	}
	got, agg := r.Linee()
	if len(got) != 2 || !agg.Equal(quando) {
		t.Fatalf("stato = %+v @ %v", got, agg)
	}
}

func TestCambiDiStato(t *testing.T) {
	r := NuovoRegistro()
	r.Aggiorna(linee("S1", trenord.Regolare, "S2", trenord.Critico, "S3", trenord.Regolare), quando)

	cambi := r.Aggiorna(linee(
		"S1", trenord.Critico, // peggiora
		"S2", trenord.Regolare, // torna a posto
		"S3", trenord.Regolare, // invariata
	), quando.Add(5*time.Minute))

	if len(cambi) != 2 {
		t.Fatalf("cambi = %+v, attesi 2", cambi)
	}
	// Lo stato di partenza deve arrivare con il cambio: "torna regolare" e
	// "diventa critica" sono due notifiche diverse.
	for _, c := range cambi {
		switch c.Linea.Codice {
		case "S1":
			if c.Prima != trenord.Regolare || c.Linea.Stato != trenord.Critico {
				t.Errorf("S1: %s -> %s", c.Prima, c.Linea.Stato)
			}
		case "S2":
			if c.Prima != trenord.Critico || c.Linea.Stato != trenord.Regolare {
				t.Errorf("S2: %s -> %s", c.Prima, c.Linea.Stato)
			}
		default:
			t.Errorf("cambio inatteso su %s", c.Linea.Codice)
		}
	}
}

// Trenord aggiunge e toglie linee. Una che compare adesso non ha un "prima",
// e annunciarla come peggiorata sarebbe inventarsi una storia; una che sparisce
// non ha nessuno a cui interessare.
func TestLineeCheVannoEVengono(t *testing.T) {
	r := NuovoRegistro()
	r.Aggiorna(linee("S1", trenord.Regolare, "S2", trenord.Critico), quando)

	cambi := r.Aggiorna(linee("S1", trenord.Regolare, "S99", trenord.Grave), quando.Add(time.Minute))
	if len(cambi) != 0 {
		t.Fatalf("cambi = %+v, atteso nessuno", cambi)
	}
	// E la linea sparita non deve restare nell'elenco servito.
	got, _ := r.Linee()
	if len(got) != 2 || got[1].Codice != "S99" {
		t.Fatalf("stato = %+v", got)
	}
}

// Una linea che oscilla fra due giri deve produrre un cambio per giro, non uno
// solo: chi ha la campanellina accesa vuole sapere anche che è tornata a posto.
func TestOscillazione(t *testing.T) {
	r := NuovoRegistro()
	r.Aggiorna(linee("S1", trenord.Regolare), quando)
	for i, atteso := range []trenord.Stato{trenord.Critico, trenord.Regolare, trenord.Grave} {
		cambi := r.Aggiorna(linee("S1", atteso), quando.Add(time.Duration(i+1)*time.Minute))
		if len(cambi) != 1 || cambi[0].Linea.Stato != atteso {
			t.Fatalf("giro %d: cambi = %+v", i, cambi)
		}
	}
}

func avviso(testo string) trenord.Avviso {
	return trenord.Avviso{Data: quando, Testo: testo}
}

func dettaglio(stato trenord.Stato, aggiornato time.Time, testi ...string) *trenord.Dettaglio {
	d := &trenord.Dettaglio{Stato: stato, Aggiornato: aggiornato}
	for _, t := range testi {
		d.Avvisi = append(d.Avvisi, avviso(t))
	}
	return d
}

// Il primo sguardo a una linea non e' una notizia: altrimenti ogni riavvio
// riannuncerebbe i lavori annunciati ad agosto e lo stato in cui la linea si
// trova in quel momento.
func TestPrimoDettaglioNonProduceNiente(t *testing.T) {
	r := NuovoRegistro()
	n := r.MettiDettaglio("S2", "Seveso", dettaglio(trenord.Critico, quando, "lavori", "sciopero"))
	if n.Cambio != nil || len(n.Avvisi) != 0 {
		t.Fatalf("novita = %+v, attesa nessuna", n)
	}
	if len(r.AvvisiDi("S2")) != 2 {
		t.Fatalf("conservati = %+v", r.AvvisiDi("S2"))
	}
}

// La ragione per cui esiste tutto questo: lo stesso indirizzo, interrogato due
// volte, risponde da backend che non concordano. Una risposta piu' vecchia di
// quella che si ha gia' non deve muovere niente, altrimenti un quarto delle
// linee sembra cambiare stato ogni pochi minuti.
func TestRispostaVecchiaScartata(t *testing.T) {
	r := NuovoRegistro()
	r.MettiDettaglio("S2", "Seveso", dettaglio(trenord.Regolare, quando, "lavori"))

	// Il backend rimasto indietro: dice un'altra cosa, ma e' vecchio.
	vecchia := r.MettiDettaglio("S2", "Seveso",
		dettaglio(trenord.Critico, quando.Add(-30*time.Minute), "lavori", "un avviso vecchio"))
	if vecchia.Cambio != nil || len(vecchia.Avvisi) != 0 {
		t.Fatalf("novita da una risposta vecchia = %+v", vecchia)
	}
	if st, _ := r.StatoNoto("S2"); st != trenord.Regolare {
		t.Errorf("stato = %v, doveva restare regolare", st)
	}
	if len(r.AvvisiDi("S2")) != 1 {
		t.Errorf("gli avvisi vecchi hanno sovrascritto: %+v", r.AvvisiDi("S2"))
	}

	// Una risposta piu' fresca invece vale.
	fresca := r.MettiDettaglio("S2", "Seveso", dettaglio(trenord.Critico, quando.Add(time.Minute), "lavori"))
	if fresca.Cambio == nil || fresca.Cambio.Prima != trenord.Regolare {
		t.Fatalf("novita = %+v, atteso il cambio", fresca)
	}
}

// L'alternanza vera, come la si e' misurata: due varianti che si scambiano a
// ogni richiesta. Alla fine deve essere annunciato un cambio solo, quello vero.
func TestAlternanzaFraBackend(t *testing.T) {
	r := NuovoRegistro()
	fresco, stantio := quando.Add(10*time.Minute), quando

	r.MettiDettaglio("S2", "Seveso", dettaglio(trenord.Regolare, stantio))
	annunci := 0
	for i, d := range []*trenord.Dettaglio{
		dettaglio(trenord.Critico, fresco),   // il backend aggiornato
		dettaglio(trenord.Regolare, stantio), // quello indietro
		dettaglio(trenord.Critico, fresco),
		dettaglio(trenord.Regolare, stantio),
		dettaglio(trenord.Critico, fresco),
	} {
		if n := r.MettiDettaglio("S2", "Seveso", d); n.Cambio != nil {
			annunci++
			if n.Cambio.Linea.Stato != trenord.Critico {
				t.Errorf("giro %d: annunciato %v", i, n.Cambio.Linea.Stato)
			}
		}
	}
	if annunci != 1 {
		t.Fatalf("annunci = %d, atteso 1: l'alternanza e' passata", annunci)
	}
}

// Un avviso nuovo e' una notizia, lo stesso avviso ripubblicato con l'ora
// aggiornata no: Trenord lo ritocca, e due notifiche per la stessa cosa sono il
// modo piu' rapido per farle spegnere.
func TestAvvisiNuovi(t *testing.T) {
	r := NuovoRegistro()
	t0 := quando
	r.MettiDettaglio("S2", "Seveso", dettaglio(trenord.Regolare, t0, "lavori"))

	n := r.MettiDettaglio("S2", "Seveso", dettaglio(trenord.Regolare, t0.Add(time.Minute), "lavori", "sciopero l'8"))
	if len(n.Avvisi) != 1 || n.Avvisi[0].Testo != "sciopero l'8" {
		t.Fatalf("avvisi nuovi = %+v", n.Avvisi)
	}
	n = r.MettiDettaglio("S2", "Seveso", dettaglio(trenord.Regolare, t0.Add(2*time.Minute), "lavori", "sciopero l'8"))
	if len(n.Avvisi) != 0 {
		t.Fatalf("riannunciato: %+v", n.Avvisi)
	}
}

// Gli avvisi di una linea non devono comparire su un'altra.
func TestAvvisiNonSiMescolano(t *testing.T) {
	r := NuovoRegistro()
	r.MettiDettaglio("S2", "Seveso", dettaglio(trenord.Regolare, quando, "guasto a Seveso"))
	r.MettiDettaglio("R16", "Asso", dettaglio(trenord.Regolare, quando, "lavori ad Asso"))

	if a := r.AvvisiDi("S2"); len(a) != 1 || a[0].Testo != "guasto a Seveso" {
		t.Errorf("S2 = %+v", a)
	}
	if len(r.AvvisiDi("S99")) != 0 {
		t.Errorf("S99 = %+v, atteso nessuno", r.AvvisiDi("S99"))
	}
}

// Le comunicazioni si chiedono solo per le linee che qualcuno segue: il
// dettaglio di una linea pesa oltre 130 KB, e senza abbonati non c'è nessuno
// per cui valga la pena chiederlo.
func TestSiInterroganoSoloLeLineeSeguite(t *testing.T) {
	tutte := linee("S1", trenord.Regolare, "S2", trenord.Critico, "R16", trenord.Grave)

	svc := Nuovo(nil)
	if n := svc.daInterrogare(tutte); len(n) != 0 {
		t.Fatalf("senza abbonati = %+v, atteso nessuna richiesta", n)
	}

	ab, _ := ApriAbbonati("")
	ab.Registra(abbonamento("https://push.example/uno", "S1"))
	svc = Nuovo(nil).ConNotifiche(ab, nil)

	scelte := svc.daInterrogare(tutte)
	// Solo S1: S2 e R16 non sono regolari, ma non le segue nessuno.
	if len(scelte) != 1 || scelte[0].Codice != "S1" {
		t.Fatalf("scelte = %+v, attesa la sola S1", scelte)
	}
}

// sorgenteAvvisiFinta conta le letture e può fingersi lenta.
type sorgenteAvvisiFinta struct {
	mu      sync.Mutex
	letture int
	ritardo time.Duration
	avvisi  []trenord.Avviso
}

func (s *sorgenteAvvisiFinta) Fetch(ctx context.Context) ([]trenord.Linea, error) {
	return linee("S2", trenord.Regolare), nil
}

func (s *sorgenteAvvisiFinta) Dettaglio(ctx context.Context, codice string) (*trenord.Dettaglio, error) {
	s.mu.Lock()
	s.letture++
	s.mu.Unlock()
	if s.ritardo > 0 {
		select {
		case <-time.After(s.ritardo):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &trenord.Dettaglio{Aggiornato: quando, Avvisi: s.avvisi}, nil
}

func (s *sorgenteAvvisiFinta) quante() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.letture
}

// Il dettaglio di una linea pesa oltre 130 KB: chi apre la stessa riga due
// volte, o dieci persone che la aprono insieme, devono produrre una lettura
// sola.
func TestAvvisiChiestiUnaVoltaSola(t *testing.T) {
	fonte := &sorgenteAvvisiFinta{avvisi: []trenord.Avviso{avviso("lavori")}, ritardo: 50 * time.Millisecond}
	svc := Nuovo(fonte)

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.ChiediDettaglio(context.Background(), "S2"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if n := fonte.quante(); n != 1 {
		t.Fatalf("letture = %d, attesa 1", n)
	}
	// E la richiesta successiva viene ancora dalla cache.
	svc.ChiediDettaglio(context.Background(), "S2")
	if n := fonte.quante(); n != 1 {
		t.Fatalf("letture = %d dopo la cache, attesa 1", n)
	}
}

// Se chi ha chiesto rinuncia, il lavoro non si butta: quello che si è letto
// resta, e il tocco dopo è immediato invece di ricominciare.
func TestChiRinunciaNonButtaIlLavoro(t *testing.T) {
	fonte := &sorgenteAvvisiFinta{avvisi: []trenord.Avviso{avviso("sciopero")}, ritardo: 150 * time.Millisecond}
	svc := Nuovo(fonte)

	ctx, annulla := context.WithCancel(context.Background())
	annulla() // il telefono se n'è andato prima ancora di cominciare

	if _, err := svc.ChiediDettaglio(ctx, "S2"); err != nil {
		t.Fatalf("la lettura doveva completare comunque: %v", err)
	}
	if a := svc.registro.AvvisiDi("S2"); len(a) != 1 {
		t.Fatalf("in cache = %+v, atteso l'avviso letto", a)
	}
}

// Il giro di lettura non si fida della cache, e deve rileggere ogni volta.
//
// Guardandola, la cadenza si dimezzava da sé: la scadenza si scrive dopo la
// lettura, quindi cade qualche secondo più tardi dell'inizio del giro, e il
// giro successivo — che parte esattamente un intervallo dopo — la trovava
// valida per un pelo. Il dettaglio si rileggeva un giro su due, cioè ogni
// dieci minuti invece di cinque: misurato in produzione l'8 settembre 2026, un
// avviso pubblicato alle 18:46 è stato riconosciuto alle 18:55.
func TestIlGiroDiLetturaRileggeSempre(t *testing.T) {
	fonte := &sorgenteAvvisiFinta{avvisi: []trenord.Avviso{avviso("lavori")}}
	ab, _ := ApriAbbonati("")
	if err := ab.Registra(abbonamento("https://push.example/uno", "S2")); err != nil {
		t.Fatal(err)
	}
	svc := Nuovo(fonte).ConNotifiche(ab, nil)
	linee := linee("S2", trenord.Regolare)

	for giro := 1; giro <= 3; giro++ {
		svc.leggiDettagli(context.Background(), linee)
		if n := fonte.quante(); n != giro {
			t.Fatalf("dopo %d giri le letture sono %d, attese %d", giro, n, giro)
		}
	}

	// Chi apre una riga nell'app, invece, la cache la usa: è lì che serve, a
	// difendere Trenord da un tocco ripetuto.
	prima := fonte.quante()
	for range 5 {
		if _, err := svc.ChiediDettaglio(context.Background(), "S2"); err != nil {
			t.Fatal(err)
		}
	}
	if n := fonte.quante(); n != prima {
		t.Errorf("letture = %d, attese %d: il tocco nell'app ha ignorato la cache", n, prima)
	}
}

// sorgenteDiscorde è la fonte come si è misurata il 9 settembre 2026: l'elenco
// diceva RE_5 grave, la pagina della linea diceva regolare e non aveva niente
// da dire. Sul sito non si vedeva niente, nell'app un bollino rosso.
type sorgenteDiscorde struct{}

func (sorgenteDiscorde) Fetch(context.Context) ([]trenord.Linea, error) {
	return linee("S2", trenord.Grave), nil
}

func (sorgenteDiscorde) Dettaglio(context.Context, string) (*trenord.Dettaglio, error) {
	return dettaglio(trenord.Regolare, quando), nil
}

// Il bollino e le comunicazioni devono venire dallo stesso foglio.
//
// L'elenco non porta un orario, quindi una risposta indietro di mezz'ora è
// indistinguibile da quella di adesso e non si può scartare; il dettaglio
// l'orario ce l'ha. Quando i due non concordano vince il dettaglio: è la stessa
// fonte da cui viene il testo che si legge aprendo la riga, ed è quella su cui
// si mandano le notifiche.
func TestIlBollinoVieneDalDettaglio(t *testing.T) {
	ab, _ := ApriAbbonati("")
	if err := ab.Registra(abbonamento("https://push.example/uno", "S2")); err != nil {
		t.Fatal(err)
	}
	svc := Nuovo(sorgenteDiscorde{}).ConNotifiche(ab, nil)
	svc.leggi(context.Background())

	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/linee", nil))
	if rr.Code != 200 {
		t.Fatalf("risposta %d: %s", rr.Code, rr.Body)
	}
	var risp struct {
		Lines []trenord.Linea `json:"lines"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &risp); err != nil {
		t.Fatal(err)
	}
	if len(risp.Lines) != 1 {
		t.Fatalf("linee = %+v", risp.Lines)
	}
	if risp.Lines[0].Stato != trenord.Regolare {
		t.Errorf("bollino = %s, atteso regolare: l'elenco vince sul dettaglio, e la riga si apre vuota",
			risp.Lines[0].Stato)
	}
}
