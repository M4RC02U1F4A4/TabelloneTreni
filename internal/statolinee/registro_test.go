package statolinee

import (
	"context"
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

// Come per i bollini, la prima lettura non è una notizia: altrimenti ogni
// riavvio riannuncerebbe i lavori annunciati ad agosto.
func TestPrimaLetturaAvvisiNonProduceNiente(t *testing.T) {
	r := NuovoRegistro()
	if n := r.MettiAvvisi("S2", []trenord.Avviso{avviso("lavori"), avviso("sciopero")}); len(n) != 0 {
		t.Fatalf("nuovi = %+v, atteso nessuno", n)
	}
	if len(r.AvvisiDi("S2")) != 2 {
		t.Fatalf("conservati = %+v", r.AvvisiDi("S2"))
	}
}

func TestAvvisiNuovi(t *testing.T) {
	r := NuovoRegistro()
	r.MettiAvvisi("S2", []trenord.Avviso{avviso("lavori")})

	nuovi := r.MettiAvvisi("S2", []trenord.Avviso{avviso("lavori"), avviso("sciopero l'8")})
	if len(nuovi) != 1 || nuovi[0].Testo != "sciopero l'8" {
		t.Fatalf("nuovi = %+v", nuovi)
	}
	// Il giro dopo non è più nuovo.
	if n := r.MettiAvvisi("S2", []trenord.Avviso{avviso("lavori"), avviso("sciopero l'8")}); len(n) != 0 {
		t.Fatalf("riannunciato: %+v", n)
	}
}

// Trenord ripubblica lo stesso avviso con l'ora aggiornata quando lo ritocca.
// Se il confronto fosse sulla data, ogni ritocco sarebbe una notifica, e due
// notifiche per la stessa cosa sono il modo più rapido per farle spegnere.
func TestStessoAvvisoConDataNuovaNonRiavvisa(t *testing.T) {
	r := NuovoRegistro()
	r.MettiAvvisi("S2", []trenord.Avviso{avviso("sciopero l'8")})

	ritoccato := trenord.Avviso{Data: quando.Add(3 * time.Hour), Testo: "sciopero l'8"}
	if n := r.MettiAvvisi("S2", []trenord.Avviso{ritoccato}); len(n) != 0 {
		t.Fatalf("riannunciato: %+v", n)
	}
}

// Gli avvisi di una linea non devono comparire su un'altra.
func TestAvvisiNonSiMescolano(t *testing.T) {
	r := NuovoRegistro()
	r.MettiAvvisi("S2", []trenord.Avviso{avviso("guasto a Seveso")})
	r.MettiAvvisi("R16", []trenord.Avviso{avviso("lavori ad Asso")})

	if len(r.AvvisiDi("S2")) != 1 || r.AvvisiDi("S2")[0].Testo != "guasto a Seveso" {
		t.Errorf("S2 = %+v", r.AvvisiDi("S2"))
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

func (s *sorgenteAvvisiFinta) Avvisi(ctx context.Context, codice string) ([]trenord.Avviso, error) {
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
	return s.avvisi, nil
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
			if _, err := svc.ChiediAvvisi(context.Background(), "S2"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if n := fonte.quante(); n != 1 {
		t.Fatalf("letture = %d, attesa 1", n)
	}
	// E la richiesta successiva viene ancora dalla cache.
	svc.ChiediAvvisi(context.Background(), "S2")
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

	if _, err := svc.ChiediAvvisi(ctx, "S2"); err != nil {
		t.Fatalf("la lettura doveva completare comunque: %v", err)
	}
	if a := svc.registro.AvvisiDi("S2"); len(a) != 1 {
		t.Fatalf("in cache = %+v, atteso l'avviso letto", a)
	}
}
