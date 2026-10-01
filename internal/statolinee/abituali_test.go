package statolinee

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/board"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/rfi"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/stations"
)

func alle(giorno, h, m int) time.Time { return time.Date(2026, 9, giorno, h, m, 0, 0, roma) }

// Il treno di oggi esiste nei giorni scelti, dal preavviso alla rete di
// sicurezza, e porta la data che ViaggiaTreno vuole: la mezzanotte italiana.
func TestIlTrenoDiOggi(t *testing.T) {
	feriale := TrenoAbituale{Origine: "S01700", Numero: "2247", Da: 1, Ora: "07:12", Giorni: []int{1, 2, 3, 4, 5}}
	for _, c := range []struct {
		nome   string
		x      TrenoAbituale
		adesso time.Time
		esiste bool
		data   time.Time
	}{
		{"un minuto prima del preavviso", feriale, alle(21, 7, 1), false, time.Time{}},
		{"al preavviso", feriale, alle(21, 7, 2), true, alle(21, 0, 0)},
		{"all'ultimo minuto", feriale, alle(21, 10, 11), true, alle(21, 0, 0)},
		{"a rete scattata", feriale, alle(21, 10, 12), false, time.Time{}},
		{"di sabato", feriale, alle(26, 7, 5), false, time.Time{}},
		{"senza giorni", TrenoAbituale{Origine: "S01700", Numero: "2247", Da: 1, Ora: "07:12"}, alle(21, 7, 5), false, time.Time{}},
		// Le finestre che scavalcano la mezzanotte: il treno delle 23:30 del
		// lunedì si segue ancora martedì all'una, quello delle 00:05 del
		// martedì si preavvisa lunedì sera.
		{"dopo mezzanotte", TrenoAbituale{Origine: "S01700", Numero: "2247", Da: 1, Ora: "23:30", Giorni: []int{1}}, alle(22, 0, 30), true, alle(21, 0, 0)},
		{"prima di mezzanotte", TrenoAbituale{Origine: "S01700", Numero: "2247", Da: 1, Ora: "00:05", Giorni: []int{2}}, alle(21, 23, 56), true, alle(22, 0, 0)},
	} {
		t.Run(c.nome, func(t *testing.T) {
			tr, _, ok := c.x.DiOggi(c.adesso)
			if ok != c.esiste {
				t.Fatalf("esiste = %v, atteso %v", ok, c.esiste)
			}
			if ok && tr.Data != c.data.UnixMilli() {
				t.Errorf("data = %v, attesa %v", time.UnixMilli(tr.Data).In(roma), c.data)
			}
		})
	}

	// Il giorno del cambio d'ora dura venticinque ore: le 7:12 restano le
	// 7:12, non le 6:12.
	t.Run("cambio dell'ora", func(t *testing.T) {
		x := TrenoAbituale{Origine: "S01700", Numero: "2247", Da: 1, Ora: "07:12", Giorni: []int{0}}
		_, parte, ok := x.DiOggi(time.Date(2026, 10, 25, 7, 5, 0, 0, roma))
		if !ok || parte.In(roma).Hour() != 7 {
			t.Errorf("parte = %v, ok = %v", parte.In(roma), ok)
		}
	})
}

// tabelloniFinti sta al posto del tabellone delle partenze, e conta le letture.
type tabelloniFinti struct {
	mu      sync.Mutex
	treni   []rfi.Train
	err     error
	letture int
}

func (f *tabelloniFinti) Get(_ context.Context, from int, _ bool, _ int) (*board.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.letture++
	if f.err != nil {
		return nil, f.err
	}
	return &board.Result{Board: &rfi.Board{PlaceID: from, Station: "TABELLONE", Trains: f.treni}}, nil
}

// abitualeDi è il 2247 delle 18:55 da quella stazione, il lunedì: lo stesso
// viaggio di trenoDi, così ViaggiaTreno finto lo conosce già.
func abitualeDi(st *stations.Station) TrenoAbituale {
	return TrenoAbituale{Origine: "S01700", Numero: "2247", Da: st.ID, Ora: "18:55", Giorni: []int{1}}
}

func prende(t *testing.T, ab *Abbonati, endpoint string, abituali ...TrenoAbituale) {
	t.Helper()
	if err := ab.Registra(Abbonamento{
		Sottoscrizione: webpush.Subscription{Endpoint: endpoint, Keys: chiaviFinte(t)},
		Abituali:       abituali,
	}); err != nil {
		t.Fatal(err)
	}
}

// giri fa girare il servizio alle ore date, una dopo l'altra.
func giri(svc *Servizio, ore ...time.Time) {
	for _, o := range ore {
		svc.orologio = func() time.Time { return o }
		svc.leggiTreni(context.Background())
	}
}

func contate(viste *[]richiesta, mu *sync.Mutex) int {
	mu.Lock()
	defer mu.Unlock()
	return len(*viste)
}

// Dieci minuti prima parte il preavviso, una volta sola. Da lì i rilevamenti
// si contano come per un segnalibro: uno nuovo è una notifica.
func TestPreavvisoUnaVoltaSolaPoiIRilevamenti(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)
	salita, _ := stazioniDiProva(t)
	ab, _ := ApriAbbonati("")
	prende(t, ab, srv.URL+"/pendolare", abitualeDi(salita))
	f := sorgenteTreniFinta()
	tab := &tabelloniFinti{treni: []rfi.Train{{Number: "2247", Category: "RE", Time: "18:55", Platform: "3"}}}
	svc := servizioTreni(t, ab, srv.Client(), f).ConTabelloni(tab)

	giri(svc, alle(21, 18, 40)) // prima della finestra: niente, nemmeno letture
	if n := f.lette(trenoDi(salita)); n != 0 || tab.letture != 0 {
		t.Fatalf("letture fuori finestra: vt %d, tabellone %d", n, tab.letture)
	}
	giri(svc, alle(21, 18, 45), alle(21, 18, 46))
	if n := contate(viste, mu); n != 1 {
		t.Fatalf("notifiche = %d, atteso il solo preavviso", n)
	}
	if tab.letture != 1 {
		t.Errorf("letture del tabellone = %d, attesa 1: preavviso già dato", tab.letture)
	}

	f.metti(trenoDi(salita), viaggio(alle(21, 18, 50), 4, fermata(salita, false)))
	giri(svc, alle(21, 18, 51))
	if n := contate(viste, mu); n != 2 {
		t.Fatalf("notifiche = %d, attese 2: il primo rilevamento dopo il preavviso si è perso", n)
	}
}

// Dopo la partenza il preavviso arriverebbe a treno passato: non parte, e il
// primo giro torna a essere muto come per un segnalibro.
func TestNienteFuoriTempoMassimo(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)
	salita, _ := stazioniDiProva(t)
	ab, _ := ApriAbbonati("")
	prende(t, ab, srv.URL+"/ritardatario", abitualeDi(salita))
	f := sorgenteTreniFinta()
	f.metti(trenoDi(salita), viaggio(alle(21, 18, 50), 4, fermata(salita, false)))
	tab := &tabelloniFinti{treni: []rfi.Train{{Number: "2247", Time: "18:55"}}}

	giri(servizioTreni(t, ab, srv.Client(), f).ConTabelloni(tab), alle(21, 18, 56))
	if n := contate(viste, mu); n != 0 || tab.letture != 0 {
		t.Errorf("notifiche = %d, letture tabellone = %d: attese zero", n, tab.letture)
	}
}

// Un tabellone che non risponde non è un treno che non c'è: si tace e si
// riprova, finché si è in tempo.
func TestTabelloneGiùSiRiprova(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)
	salita, _ := stazioniDiProva(t)
	ab, _ := ApriAbbonati("")
	prende(t, ab, srv.URL+"/paziente", abitualeDi(salita))
	tab := &tabelloniFinti{err: errors.New("RFI non risponde")}
	svc := servizioTreni(t, ab, srv.Client(), sorgenteTreniFinta()).ConTabelloni(tab)

	giri(svc, alle(21, 18, 45))
	if n := contate(viste, mu); n != 0 {
		t.Fatalf("notifiche = %d con il tabellone giù", n)
	}
	tab.mu.Lock()
	tab.err = nil
	tab.mu.Unlock()
	giri(svc, alle(21, 18, 46))
	if n := contate(viste, mu); n != 1 {
		t.Errorf("notifiche = %d, atteso il preavviso al secondo tentativo", n)
	}
}

// Arrivato dove si sale, il treno di oggi è chiuso: non rinasce al giro dopo,
// non si rilegge più, e l'abbonamento resta per domani.
func TestAbitualeArrivatoNonRinasce(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)
	salita, _ := stazioniDiProva(t)
	ab, _ := ApriAbbonati("")
	prende(t, ab, srv.URL+"/a-bordo", abitualeDi(salita))
	f := sorgenteTreniFinta()
	f.metti(trenoDi(salita), viaggio(alle(21, 18, 50), 0, fermata(salita, false)))
	svc := servizioTreni(t, ab, srv.Client(), f)

	giri(svc, alle(21, 18, 50)) // primo giro: muto (niente tabellone, niente preavviso)
	f.metti(trenoDi(salita), viaggio(alle(21, 18, 56), 1, fermata(salita, true)))
	giri(svc, alle(21, 18, 56), alle(21, 18, 57), alle(21, 18, 58))

	if n := contate(viste, mu); n != 1 {
		t.Errorf("notifiche = %d, attesa 1 (l'arrivo)", n)
	}
	if n := f.lette(trenoDi(salita)); n != 2 {
		t.Errorf("letture = %d, attese 2: il treno di oggi è rinato", n)
	}
	if len(ab.Tutti()) != 1 {
		t.Error("l'abbonamento con soli abituali se n'è andato con il treno di oggi")
	}
}

func TestIlTestoDelPreavviso(t *testing.T) {
	salita, _ := stazioniDiProva(t)
	tr, parte, _ := abitualeDi(salita).DiOggi(alle(21, 18, 45))
	a := attesa{TrenoSeguito: tr, parte: parte}
	misurato := 7
	for nome, c := range map[string]struct {
		treni  []rfi.Train
		titolo string
		corpo  string
	}{
		"in orario": {[]rfi.Train{{Number: "2247", Category: "RE", Time: "18:55", Platform: "3"}},
			"RE 2247 delle 18:55", "Bin. 3, in orario."},
		"due ritardi": {[]rfi.Train{{Number: "2247", Category: "RE", Time: "18:55", Platform: "3", Delay: 5, LiveDelay: &misurato}},
			"RE 2247 delle 18:55", "Bin. 3, +5 min (misurato +7 min)."},
		"binario cambiato": {[]rfi.Train{{Number: "2247", Time: "18:55", Platform: "4", PlatformChanged: true, PlatformScheduled: "2", PlatformActual: "4"}},
			"Treno 2247 delle 18:55", "Bin. 4, non il 2, in orario."},
		"soppresso": {[]rfi.Train{{Number: "2247", Category: "RE", Time: "18:55", Cancelled: true}},
			"RE 2247 delle 18:55", "Soppresso."},
		"ritardo annunciato": {[]rfi.Train{{Number: "2247", Time: "18:55", Status: "RITARDO"}},
			"Treno 2247 delle 18:55", "Binario non ancora assegnato, ritardo."},
		// Sdoppiato: due righe con lo stesso numero, vale quella dell'orario.
		"sdoppiato": {[]rfi.Train{{Number: "2247", Time: "19:40", Platform: "9"}, {Number: "2247", Time: "18:55", Platform: "3"}},
			"Treno 2247 delle 18:55", "Bin. 3, in orario."},
		"assente": {nil, "Treno 2247 delle 18:55", "Non compare sul tabellone di " + salita.Name + "."},
	} {
		t.Run(nome, func(t *testing.T) {
			m := messaggioPreavviso(a, &rfi.Board{Trains: c.treni})
			if m.Titolo != c.titolo || m.Corpo != c.corpo {
				t.Errorf("= %q / %q\natteso %q / %q", m.Titolo, m.Corpo, c.titolo, c.corpo)
			}
			if !strings.HasPrefix(m.Tag, "treno-") || !strings.HasSuffix(m.Tag, tr.Chiave()) {
				t.Errorf("tag = %q: i rilevamenti non lo sostituirebbero", m.Tag)
			}
		})
	}
}

func TestAbitualeMalformatoSiRifiuta(t *testing.T) {
	buono := TrenoAbituale{Origine: "S01700", Numero: "2247", Da: 1, Ora: "07:12", Giorni: []int{1}}
	for nome, cambia := range map[string]func(*TrenoAbituale){
		"origine inventata": func(x *TrenoAbituale) { x.Origine = "../../etc" },
		"senza stazione":    func(x *TrenoAbituale) { x.Da = 0 },
		"ora storta":        func(x *TrenoAbituale) { x.Ora = "7:12" },
		"giorno otto":       func(x *TrenoAbituale) { x.Giorni = []int{7} },
	} {
		t.Run(nome, func(t *testing.T) {
			x := buono
			cambia(&x)
			ab, _ := ApriAbbonati("")
			if err := ab.Registra(Abbonamento{
				Sottoscrizione: webpush.Subscription{Endpoint: "https://push.example/x", Keys: chiaviFinte(t)},
				Abituali:       []TrenoAbituale{x},
			}); err == nil {
				t.Fatal("accettato")
			}
		})
	}
}
