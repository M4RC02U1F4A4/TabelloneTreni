package statolinee

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/stations"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/vt"
)

// treniFinti sta al posto di ViaggiaTreno e conta quante volte gli si chiede
// ogni treno: la deduplica è metà del disegno, e senza contare le letture non
// si dimostra.
type treniFinti struct {
	mu      sync.Mutex
	viaggi  map[string]*vt.Andamento
	letture map[string]int
	// Quanto restava, a ogni lettura, prima che il contesto scadesse: zero se
	// il contesto non scadeva affatto.
	attese []time.Duration
}

func (f *treniFinti) Andamento(ctx context.Context, o, n string, d int64) (*vt.Andamento, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var attesa time.Duration
	if scade, ok := ctx.Deadline(); ok {
		attesa = time.Until(scade)
	}
	f.attese = append(f.attese, attesa)
	k := TrenoSeguito{Origine: o, Numero: n, Data: d}.Chiave()
	f.letture[k]++
	return f.viaggi[k], nil
}

func (f *treniFinti) metti(t TrenoSeguito, a *vt.Andamento) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.viaggi[t.Chiave()] = a
}

func (f *treniFinti) lette(t TrenoSeguito) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.letture[t.Chiave()]
}

func sorgenteTreniFinta() *treniFinti {
	return &treniFinti{viaggi: map[string]*vt.Andamento{}, letture: map[string]int{}}
}

// stazioniDiProva prende dal catalogo vero due stazioni che ViaggiaTreno
// conosce. Devono essere vere: il servizio traduce l'id RFI in codici VT con
// quel catalogo, e una stazione inventata non si tradurrebbe — il test
// passerebbe per il motivo sbagliato, cioè perché la fermata non si trova mai.
func stazioniDiProva(t *testing.T) (a, b *stations.Station) {
	t.Helper()
	for _, s := range stations.Default.Elenco {
		if s.VT == "" {
			continue
		}
		if a == nil {
			a = s
			continue
		}
		return a, s
	}
	t.Fatal("il catalogo non ha due stazioni con codice ViaggiaTreno")
	return nil, nil
}

// partenza è la mezzanotte del giorno di partenza, come la manda il telefono.
var partenza = time.Date(2026, 9, 21, 0, 0, 0, 0, roma).UnixMilli()

// adessoDiProva è l'ora dei giri nei test: la sera di quel giorno. Ferma e non
// time.Now, altrimenti la rete delle trentasei ore butta fuori i treni dei test
// appena passa una settimana da quando sono stati scritti.
var adessoDiProva = time.Date(2026, 9, 21, 18, 0, 0, 0, roma)

func trenoDi(st *stations.Station) TrenoSeguito {
	return TrenoSeguito{Origine: "S01700", Numero: "2247", Data: partenza, Da: st.ID}
}

// viaggio è un andamento con una fermata sola, quella che interessa al test.
func viaggio(visto time.Time, ritardo int, f vt.Fermata) *vt.Andamento {
	return &vt.Andamento{
		CodOrigine: "S01700", Numero: "2247", DataPartenza: partenza,
		Categoria: "RE", Destinazione: "Milano Centrale",
		Ritardo: ritardo, Stazione: "Rho Fiera", Ora: visto,
		Fermate: []vt.Fermata{f},
	}
}

func fermata(st *stations.Station, passata bool) vt.Fermata {
	return vt.Fermata{
		Codice: st.VT, Nome: st.Name, Passata: passata,
		Programmata: time.Date(2026, 9, 21, 18, 55, 0, 0, roma),
	}
}

// servizioTreni monta il servizio come in produzione, ma con ViaggiaTreno e il
// servizio push finti.
func servizioTreni(t *testing.T, ab *Abbonati, c *http.Client, f *treniFinti) *Servizio {
	t.Helper()
	s := Nuovo(nil).ConNotifiche(ab, notificatoreDiProva(t, ab, c)).ConTreni(f)
	s.orologio = func() time.Time { return adessoDiProva }
	return s
}

func aspetta(t *testing.T, ab *Abbonati, endpoint string, treni ...TrenoSeguito) {
	t.Helper()
	if err := ab.Registra(Abbonamento{
		Sottoscrizione: webpush.Subscription{Endpoint: endpoint, Keys: chiaviFinte(t)},
		Treni:          treni,
	}); err != nil {
		t.Fatal(err)
	}
}

func treniDi(ab *Abbonati, endpoint string) []TrenoSeguito {
	for _, a := range ab.Tutti() {
		if a.Sottoscrizione.Endpoint == endpoint {
			return a.Treni
		}
	}
	return nil
}

// Il client di ViaggiaTreno aspetta il viaggio fino a venti secondi, perché per
// la scheda sul telefono quella è l'unica lettura. Il giro delle notifiche
// invece legge in fila dentro un minuto: ogni treno resta agli otto secondi,
// altrimenti tre treni bloccati basterebbero a far saltare il giro a tutti gli
// altri.
func TestOgniViaggioDelGiroAspettaOttoSecondi(t *testing.T) {
	srv, _, _ := servizioPushFinto(t, http.StatusCreated)
	salita, _ := stazioniDiProva(t)
	tr := trenoDi(salita)

	ab, _ := ApriAbbonati("")
	aspetta(t, ab, srv.URL+"/aspetta", tr)
	f := sorgenteTreniFinta()
	f.metti(tr, viaggio(oreDi(18), 7, fermata(salita, false)))

	servizioTreni(t, ab, srv.Client(), f).leggiTreni(context.Background())

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.attese) == 0 {
		t.Fatal("nessuna lettura")
	}
	for _, a := range f.attese {
		if a <= 0 || a > 8*time.Second {
			t.Errorf("lettura con %v davanti: attesi al più otto secondi", a)
		}
	}
}

// Il primo giro prende nota e tace: il segnalibro si mette guardando la scheda
// del treno, e notificare quello che si sta già leggendo sarebbe una suoneria
// di benvenuto.
func TestPrimoGiroSulTrenoNonNotifica(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)
	salita, _ := stazioniDiProva(t)
	tr := trenoDi(salita)

	ab, _ := ApriAbbonati("")
	aspetta(t, ab, srv.URL+"/aspetta", tr)
	f := sorgenteTreniFinta()
	f.metti(tr, viaggio(oreDi(18), 7, fermata(salita, false)))

	servizioTreni(t, ab, srv.Client(), f).leggiTreni(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(*viste) != 0 {
		t.Fatalf("notifiche = %d, attese 0: %+v", len(*viste), *viste)
	}
	if v := ab.Tutti()[0].VistoTreni[tr.Chiave()]; v.Rilevamento == 0 {
		t.Error("il primo giro non ha preso nota: la notizia dopo si perderebbe")
	}
}

// Un rilevamento nuovo è una notifica, ed è la regola centrale di tutto il
// disegno. Lo stesso rilevamento riletto non lo è.
func TestOgniRilevamentoNotificaUnaVoltaSola(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)
	salita, _ := stazioniDiProva(t)
	tr := trenoDi(salita)

	ab, _ := ApriAbbonati("")
	aspetta(t, ab, srv.URL+"/aspetta", tr)
	f := sorgenteTreniFinta()
	f.metti(tr, viaggio(oreDi(18), 7, fermata(salita, false)))
	svc := servizioTreni(t, ab, srv.Client(), f)

	svc.leggiTreni(context.Background()) // primo giro: muto
	f.metti(tr, viaggio(oreDi(18).Add(6*time.Minute), 9, fermata(salita, false)))
	svc.leggiTreni(context.Background()) // rilevamento nuovo: una notifica
	svc.leggiTreni(context.Background()) // stesso rilevamento: niente

	mu.Lock()
	defer mu.Unlock()
	if len(*viste) != 1 {
		t.Fatalf("notifiche = %d, attesa 1: %+v", len(*viste), *viste)
	}
}

// Un binario assegnato non muove l'orologio dei rilevamenti, e tacerlo
// sarebbe il contrario di "tutti gli aggiornamenti": è anzi la cosa che si
// aspetta chi è in stazione.
func TestBinarioAssegnatoNotificaAOrologioFermo(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)
	salita, _ := stazioniDiProva(t)
	tr := trenoDi(salita)

	ab, _ := ApriAbbonati("")
	aspetta(t, ab, srv.URL+"/aspetta", tr)
	f := sorgenteTreniFinta()
	f.metti(tr, viaggio(oreDi(18), 7, fermata(salita, false)))
	svc := servizioTreni(t, ab, srv.Client(), f)

	svc.leggiTreni(context.Background())
	conBinario := fermata(salita, false)
	conBinario.BinarioEffettivo = "4"
	f.metti(tr, viaggio(oreDi(18), 7, conBinario)) // stesso rilevamento, stesso ritardo
	svc.leggiTreni(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(*viste) != 1 {
		t.Fatalf("notifiche = %d, attesa 1: %+v", len(*viste), *viste)
	}
}

// La fine della finestra: il treno arriva dove si sale, quella notifica parte,
// e il treno esce dall'elenco — che è anche ciò che ferma il polling.
func TestArrivatoDoveSaliMandaLUltimaENonSeguePiu(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)
	salita, _ := stazioniDiProva(t)
	tr := trenoDi(salita)

	ab, _ := ApriAbbonati("")
	aspetta(t, ab, srv.URL+"/aspetta", tr)
	f := sorgenteTreniFinta()
	f.metti(tr, viaggio(oreDi(18), 7, fermata(salita, false)))
	svc := servizioTreni(t, ab, srv.Client(), f)

	svc.leggiTreni(context.Background())
	arrivata := fermata(salita, true)
	arrivata.Effettiva = time.Date(2026, 9, 21, 19, 2, 0, 0, roma)
	arrivata.Ritardo = 7
	f.metti(tr, viaggio(oreDi(19), 7, arrivata))
	svc.leggiTreni(context.Background())

	mu.Lock()
	if len(*viste) != 1 {
		t.Fatalf("notifiche = %d, attesa 1 (l'ultima): %+v", len(*viste), *viste)
	}
	mu.Unlock()

	// Niente più treni e nessuna campanella: l'abbonamento non ha più motivo
	// di esistere, come per chi spegne l'ultima campanella.
	if len(ab.Tutti()) != 0 {
		t.Fatalf("abbonamento ancora vivo con %+v", treniDi(ab, srv.URL+"/aspetta"))
	}
	svc.leggiTreni(context.Background())
	if n := f.lette(tr); n != 2 {
		t.Errorf("letture = %d, attese 2: il polling non si è fermato", n)
	}
}

// Chi mette il segnalibro a treno già passato dalla sua stazione è a bordo e
// lo segue per vedere quando arriva: ha detto lui che da lì in poi non gli
// interessa. Nessuna notifica, e il servizio smette subito di leggerlo.
func TestSegnalibroATrenoGiaPassatoNonNotificaMai(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)
	salita, _ := stazioniDiProva(t)
	tr := trenoDi(salita)

	ab, _ := ApriAbbonati("")
	aspetta(t, ab, srv.URL+"/aspetta", tr)
	f := sorgenteTreniFinta()
	f.metti(tr, viaggio(oreDi(19), 7, fermata(salita, true)))

	servizioTreni(t, ab, srv.Client(), f).leggiTreni(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(*viste) != 0 {
		t.Fatalf("notifiche = %d, attese 0: %+v", len(*viste), *viste)
	}
	if len(ab.Tutti()) != 0 {
		t.Error("il treno è rimasto nell'elenco: verrebbe riletto per sempre")
	}
}

// Il punto di tutto: il costo cresce con i treni, non con i telefoni.
func TestStessoTrenoDueAbbonatiUnaLetturaSola(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)
	salita, _ := stazioniDiProva(t)
	tr := trenoDi(salita)

	ab, _ := ApriAbbonati("")
	aspetta(t, ab, srv.URL+"/uno", tr)
	aspetta(t, ab, srv.URL+"/due", tr)
	f := sorgenteTreniFinta()
	f.metti(tr, viaggio(oreDi(18), 7, fermata(salita, false)))
	svc := servizioTreni(t, ab, srv.Client(), f)

	svc.leggiTreni(context.Background())
	f.metti(tr, viaggio(oreDi(18).Add(6*time.Minute), 9, fermata(salita, false)))
	svc.leggiTreni(context.Background())

	if n := f.lette(tr); n != 2 {
		t.Errorf("letture = %d, attese 2 (una per giro, non una per telefono)", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*viste) != 2 {
		t.Fatalf("notifiche = %d, attese 2 (una a testa): %+v", len(*viste), *viste)
	}
}

// Due persone sullo stesso treno che salgono in due posti diversi sono a due
// punti diversi della storia: ognuna smette alla propria fermata, e l'altra
// continua.
func TestOgnunoSmetteAllaPropriaFermata(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)
	prima, dopo := stazioniDiProva(t)
	trPrima, trDopo := trenoDi(prima), trenoDi(dopo)

	ab, _ := ApriAbbonati("")
	aspetta(t, ab, srv.URL+"/scende-prima", trPrima)
	aspetta(t, ab, srv.URL+"/scende-dopo", trDopo)

	f := sorgenteTreniFinta()
	a := viaggio(oreDi(18), 7, fermata(prima, false))
	a.Fermate = append(a.Fermate, fermata(dopo, false))
	f.metti(trPrima, a)
	svc := servizioTreni(t, ab, srv.Client(), f)
	svc.leggiTreni(context.Background())

	// Il treno passa dalla prima stazione e non ancora dalla seconda.
	b := viaggio(oreDi(19), 7, fermata(prima, true))
	b.Fermate = append(b.Fermate, fermata(dopo, false))
	f.metti(trPrima, b)
	svc.leggiTreni(context.Background())

	mu.Lock()
	if len(*viste) != 2 {
		t.Fatalf("notifiche = %d, attese 2: %+v", len(*viste), *viste)
	}
	mu.Unlock()
	if n := len(treniDi(ab, srv.URL+"/scende-prima")); n != 0 {
		t.Errorf("chi sale alla prima segue ancora %d treni", n)
	}
	if n := len(treniDi(ab, srv.URL+"/scende-dopo")); n != 1 {
		t.Errorf("chi sale alla seconda segue %d treni, atteso 1", n)
	}
}

// Il treno di ieri: ViaggiaTreno se lo dimentica, e senza questa rete di
// sicurezza resterebbe nell'elenco a farsi interrogare per sempre.
func TestTrenoScadutoEsceDallElenco(t *testing.T) {
	srv, _, _ := servizioPushFinto(t, http.StatusCreated)
	salita, _ := stazioniDiProva(t)
	vecchio := trenoDi(salita)
	vecchio.Data = adessoDiProva.Add(-48 * time.Hour).UnixMilli()

	ab, _ := ApriAbbonati("")
	aspetta(t, ab, srv.URL+"/vecchio", vecchio)
	f := sorgenteTreniFinta()
	// Con un altro treno vivo accanto, per non confondere "potato" con "giro
	// saltato perché non c'era niente da leggere".
	vivo := trenoDi(salita)
	aspetta(t, ab, srv.URL+"/vecchio", vecchio, vivo)
	f.metti(vivo, viaggio(oreDi(18), 0, fermata(salita, false)))

	servizioTreni(t, ab, srv.Client(), f).leggiTreni(context.Background())

	rimasti := treniDi(ab, srv.URL+"/vecchio")
	if len(rimasti) != 1 || rimasti[0].Chiave() != vivo.Chiave() {
		t.Errorf("rimasti = %+v, atteso il solo treno vivo", rimasti)
	}
}

// La regressione che preoccupa di più: prima dei treni, un abbonamento senza
// linee si cancellava. Chi segue un treno senza avere nessuna campanella
// accesa è il caso più comune di tutti, e si cancellerebbe da sé al primo
// invio.
func TestAbbonamentoConSoliTreniSopravvive(t *testing.T) {
	salita, _ := stazioniDiProva(t)
	ab, _ := ApriAbbonati("")
	aspetta(t, ab, "https://push.example/solo-treni", trenoDi(salita))

	if len(ab.Tutti()) != 1 {
		t.Fatal("l'abbonamento con soli treni è stato cancellato")
	}
	// E spegnendo tutto se ne va, come chi spegne l'ultima campanella.
	ab.Registra(Abbonamento{Sottoscrizione: webpush.Subscription{
		Endpoint: "https://push.example/solo-treni", Keys: chiaviFinte(t)}})
	if len(ab.Tutti()) != 0 {
		t.Error("svuotato di tutto, l'abbonamento è rimasto")
	}
}

// Le coordinate finiscono in un URL verso ViaggiaTreno: si accetta la forma
// dei codici veri, non quello che arriva.
func TestTrenoMalformatoSiRifiuta(t *testing.T) {
	for nome, tr := range map[string]TrenoSeguito{
		"origine inventata": {Origine: "../../etc", Numero: "2247", Data: partenza},
		"numero strano":     {Origine: "S01700", Numero: "22 47", Data: partenza},
		"senza giorno":      {Origine: "S01700", Numero: "2247"},
	} {
		t.Run(nome, func(t *testing.T) {
			ab, _ := ApriAbbonati("")
			err := ab.Registra(Abbonamento{
				Sottoscrizione: webpush.Subscription{Endpoint: "https://push.example/x", Keys: chiaviFinte(t)},
				Treni:          []TrenoSeguito{tr},
			})
			if err == nil {
				t.Fatal("accettato")
			}
		})
	}
}

// Il tetto sul giro taglia sempre gli stessi treni, non un insieme diverso a
// ogni lettura: senza un ordine stabile, i treni oltre il tetto sarebbero
// seguiti a intermittenza invece che non seguiti.
func TestUnioneDeiTreniEStabile(t *testing.T) {
	salita, _ := stazioniDiProva(t)
	ab, _ := ApriAbbonati("")
	for i, n := range []string{"2247", "1234", "9999"} {
		tr := trenoDi(salita)
		tr.Numero = n
		aspetta(t, ab, "https://push.example/"+n, tr)
		_ = i
	}
	primo := ab.TreniSeguiti(adessoDiProva)
	if len(primo) != 3 {
		t.Fatalf("unione = %d, attesi 3", len(primo))
	}
	for range 5 {
		if !slices.Equal(chiavi(ab.TreniSeguiti(adessoDiProva)), chiavi(primo)) {
			t.Fatal("l'ordine dell'unione cambia fra un giro e l'altro")
		}
	}
}

func chiavi(ts []TrenoSeguito) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Chiave())
	}
	return out
}

// Il testo è l'unica parte che qualcuno legge davvero, e passa cifrata: il
// servizio push finto non può controllarlo, quindi lo si guarda qui.
func TestIlTestoDellaNotifica(t *testing.T) {
	salita, _ := stazioniDiProva(t)
	tr := trenoDi(salita)
	alle := func(h, m int) time.Time { return time.Date(2026, 9, 21, h, m, 0, 0, roma) }

	t.Run("in viaggio", func(t *testing.T) {
		f := fermata(salita, false)
		f.BinarioEffettivo = "4"
		m := messaggioTreno(viaggio(alle(18, 42), 7, f), tr, &f)
		if m.Titolo != "RE 2247 → Milano Centrale" {
			t.Errorf("titolo = %q", m.Titolo)
		}
		atteso := "Visto a Rho Fiera alle 18:42, +7 min. A " + salita.Name + " alle 18:55, bin. 4."
		if m.Corpo != atteso {
			t.Errorf("corpo  = %q\natteso = %q", m.Corpo, atteso)
		}
		if m.Tag != "treno-S01700|2247|"+strconv.FormatInt(partenza, 10) {
			t.Errorf("tag = %q: due rilevamenti si impilerebbero", m.Tag)
		}
		if m.URL != "./#/t/S01700/2247/"+strconv.FormatInt(partenza, 10) {
			t.Errorf("url = %q: il tocco non aprirebbe la scheda del treno", m.URL)
		}
	})

	// Nessun rilevamento: "visto a" senza un posto sarebbe una riga che non
	// significa niente.
	t.Run("non ancora partito", func(t *testing.T) {
		a := viaggio(time.Time{}, 0, fermata(salita, false))
		a.Stazione = ""
		f := fermata(salita, false)
		if m := messaggioTreno(a, tr, &f); !strings.HasPrefix(m.Corpo, "Non ancora partito, in orario.") {
			t.Errorf("corpo = %q", m.Corpo)
		}
	})

	// L'ultima notifica del treno: una notizia sola, non la stessa cosa detta
	// in due modi.
	t.Run("arrivato dove sali", func(t *testing.T) {
		f := fermata(salita, true)
		f.Effettiva, f.Ritardo, f.BinarioEffettivo = alle(19, 2), 7, "4"
		m := messaggioTreno(viaggio(alle(19, 2), 7, f), tr, &f)
		atteso := "È arrivato a " + salita.Name + " alle 19:02, +7 min, bin. 4."
		if m.Corpo != atteso {
			t.Errorf("corpo  = %q\natteso = %q", m.Corpo, atteso)
		}
	})

	t.Run("con provvedimento", func(t *testing.T) {
		a := viaggio(alle(18, 42), 7, fermata(salita, false))
		a.ConProvvedimento = true
		f := fermata(salita, false)
		if m := messaggioTreno(a, tr, &f); !strings.HasPrefix(m.Corpo, "Provvedimento sul treno. ") {
			t.Errorf("corpo = %q: la cosa più importante non è in testa", m.Corpo)
		}
	})

	// Stazione che il catalogo non traduce o treno che lì non ferma: resta la
	// sola riga sul treno, senza una frase su un posto di cui non si sa niente.
	t.Run("fermata sconosciuta", func(t *testing.T) {
		m := messaggioTreno(viaggio(alle(18, 42), -1, fermata(salita, false)), tr, nil)
		if m.Corpo != "Visto a Rho Fiera alle 18:42, -1 min." {
			t.Errorf("corpo = %q", m.Corpo)
		}
	})
}
