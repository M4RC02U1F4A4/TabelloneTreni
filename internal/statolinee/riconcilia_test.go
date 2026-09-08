package statolinee

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/trenord"
)

// mattina e pomeriggio sono le fasce di chi lavora: andata e ritorno.
var (
	mattina    = Fascia{Da: "07:00", A: "09:00"}
	pomeriggio = Fascia{Da: "17:00", A: "19:00"}
)

// abbonaA registra un telefono che segue S2 con le fasce date, e restituisce il
// percorso su cui contare le notifiche che gli arrivano.
func abbonaA(t *testing.T, ab *Abbonati, base, nome string, fasce ...Fascia) string {
	t.Helper()
	if err := ab.Registra(Abbonamento{
		Sottoscrizione: webpush.Subscription{Endpoint: base + "/" + nome, Keys: chiaviFinte(t)},
		Linee:          []string{"S2"},
		Fasce:          fasce,
	}); err != nil {
		t.Fatal(err)
	}
	return "/" + nome
}

func quante(viste *[]richiesta, percorso string) int {
	n := 0
	for _, r := range *viste {
		if r.percorso == percorso {
			n++
		}
	}
	return n
}

// Il caso da cui è nata la funzione: un guasto che compare alle 6, quando
// nessuno ascolta, e alle 7 è ancora lì. Va raccontato alle 7.
//
// Non c'è nessuna coda di notifiche rimandate: alle 7 il servizio sa com'è la
// linea adesso e sa cosa ha già raccontato, e la differenza fra le due cose è
// tutto quello che serve.
func TestUnGuastoAncoraLiAllAperturaVieneRaccontato(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)
	ab, _ := ApriAbbonati("")
	dove := abbonaA(t, ab, srv.URL, "lavoratore", mattina, pomeriggio)
	n := notificatoreDiProva(t, ab, srv.Client())

	reg := registroCon(t, "S2", trenord.Regolare)
	// Le 5: si prende nota che la linea è regolare, in silenzio.
	n.Riconcilia(context.Background(), reg, oreDi(5))

	// Le 6: la linea si guasta, ma la fascia è chiusa.
	metti(reg, "S2", trenord.Critico, 2)
	n.Riconcilia(context.Background(), reg, oreDi(6))
	mu.Lock()
	if q := quante(viste, dove); q != 0 {
		mu.Unlock()
		t.Fatalf("alle 6 sono arrivate %d notifiche", q)
	}
	mu.Unlock()

	// Le 7: il guasto è ancora lì, e la fascia si apre.
	metti(reg, "S2", trenord.Critico, 3)
	n.Riconcilia(context.Background(), reg, oreDi(7))
	mu.Lock()
	defer mu.Unlock()
	if q := quante(viste, dove); q != 1 {
		t.Fatalf("alle 7 le notifiche sono %d, attesa 1", q)
	}
}

// L'altra faccia della stessa regola: un guasto comparso e rientrato mentre
// nessuno ascoltava non è più una notizia. Rimandare non vuol dire accumulare.
func TestUnGuastoRientratoPrimaDellAperturaNonArriva(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)
	ab, _ := ApriAbbonati("")
	dove := abbonaA(t, ab, srv.URL, "lavoratore", mattina)
	n := notificatoreDiProva(t, ab, srv.Client())

	reg := registroCon(t, "S2", trenord.Regolare)
	n.Riconcilia(context.Background(), reg, oreDi(5))

	metti(reg, "S2", trenord.Grave, 2) // le 6: si guasta
	n.Riconcilia(context.Background(), reg, oreDi(6))
	metti(reg, "S2", trenord.Regolare, 3) // le 6 e mezza: rientra
	n.Riconcilia(context.Background(), reg, oreDi(6))

	// Le 7: la linea sta come l'abbonato l'ha lasciata. Niente da dire.
	metti(reg, "S2", trenord.Regolare, 4)
	n.Riconcilia(context.Background(), reg, oreDi(7))

	mu.Lock()
	defer mu.Unlock()
	if q := quante(viste, dove); q != 0 {
		t.Fatalf("notifiche = %d, attesa nessuna", q)
	}
}

// Dentro la fascia le cose arrivano quando succedono, come prima.
func TestDentroLaFasciaArrivaSubito(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)
	ab, _ := ApriAbbonati("")
	dove := abbonaA(t, ab, srv.URL, "lavoratore", mattina)
	n := notificatoreDiProva(t, ab, srv.Client())

	reg := registroCon(t, "S2", trenord.Regolare)
	n.Riconcilia(context.Background(), reg, oreDi(8))
	metti(reg, "S2", trenord.Critico, 2)
	n.Riconcilia(context.Background(), reg, oreDi(8))

	mu.Lock()
	defer mu.Unlock()
	if q := quante(viste, dove); q != 1 {
		t.Fatalf("notifiche = %d, attesa 1", q)
	}
}

// Due persone con fasce diverse non sono allo stesso punto della storia, ed è
// il motivo per cui la memoria sta per abbonato e non una volta per tutti: lo
// stesso guasto è una notizia alle 8 per uno e alle 18 per l'altro, e con un
// solo "ultimo stato noto" il secondo non l'avrebbe saputo mai.
func TestDuePersoneConFasceDiverseSentonoLaStessaCosaAOreDiverse(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)
	ab, _ := ApriAbbonati("")
	presto := abbonaA(t, ab, srv.URL, "mattiniero", mattina)
	tardi := abbonaA(t, ab, srv.URL, "serale", pomeriggio)
	n := notificatoreDiProva(t, ab, srv.Client())

	reg := registroCon(t, "S2", trenord.Regolare)
	// Il punto di partenza si prende per tutti al primo giro, fascia aperta o
	// chiusa: è quello che permette al serale di ricevere alle 18 un guasto
	// comparso alle 8.
	n.Riconcilia(context.Background(), reg, oreDi(8))

	metti(reg, "S2", trenord.Critico, 2)
	n.Riconcilia(context.Background(), reg, oreDi(8))
	mu.Lock()
	if q, s := quante(viste, presto), quante(viste, tardi); q != 1 || s != 0 {
		mu.Unlock()
		t.Fatalf("alle 8: mattiniero = %d (attesa 1), serale = %d (attesa 0)", q, s)
	}
	mu.Unlock()

	// Alle 18 il guasto è ancora lì. Per il serale è nuovo, per il mattiniero
	// non è cambiato niente da quando gliel'hanno detto.
	metti(reg, "S2", trenord.Critico, 3)
	n.Riconcilia(context.Background(), reg, oreDi(18))
	mu.Lock()
	defer mu.Unlock()
	if q, s := quante(viste, presto), quante(viste, tardi); q != 1 || s != 1 {
		t.Fatalf("alle 18: mattiniero = %d (attesa 1), serale = %d (attesa 1)", q, s)
	}
}

// L'applicazione riallinea l'abbonamento a ogni avvio. Se quel gesto azzerasse
// la memoria, ogni riapertura riporterebbe le linee al "primo giro" — che per
// regola tace — e la notizia successiva si perderebbe in silenzio.
func TestRiabbonarsiNonAzzeraLaMemoria(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)
	ab, _ := ApriAbbonati("")
	chiavi := chiaviFinte(t)
	endpoint := srv.URL + "/lavoratore"
	riabbona := func() {
		if err := ab.Registra(Abbonamento{
			Sottoscrizione: webpush.Subscription{Endpoint: endpoint, Keys: chiavi},
			Linee:          []string{"S2"},
			Fasce:          []Fascia{mattina},
		}); err != nil {
			t.Fatal(err)
		}
	}
	riabbona()
	n := notificatoreDiProva(t, ab, srv.Client())

	reg := registroCon(t, "S2", trenord.Regolare)
	n.Riconcilia(context.Background(), reg, oreDi(8))

	riabbona() // l'app si riapre

	metti(reg, "S2", trenord.Critico, 2)
	n.Riconcilia(context.Background(), reg, oreDi(8))

	mu.Lock()
	defer mu.Unlock()
	if q := quante(viste, "/lavoratore"); q != 1 {
		t.Fatalf("notifiche = %d, attesa 1: la memoria è stata azzerata", q)
	}
}

// Chi spegne una campanella e la riaccende dopo ricomincia da capo: di quel
// tempo non gli è stato raccontato niente, e riprendere il filo da dove era
// vorrebbe dire annunciargli un cambio vecchio di settimane.
func TestSpegnereLaCampanellaDimenticaLaLinea(t *testing.T) {
	ab, _ := ApriAbbonati("")
	chiavi := chiaviFinte(t)
	e := "https://push.example/uno"
	registra := func(linee ...string) {
		if err := ab.Registra(Abbonamento{
			Sottoscrizione: webpush.Subscription{Endpoint: e, Keys: chiavi},
			Linee:          linee,
		}); err != nil {
			t.Fatal(err)
		}
	}
	registra("S2", "R16")
	ab.SegnaVisto(e, map[string]Visto{
		"S2":  {Stato: trenord.Critico},
		"R16": {Stato: trenord.Grave},
	})

	registra("S2") // spenta la campanella su R16

	var visto map[string]Visto
	for _, a := range ab.Tutti() {
		if a.Sottoscrizione.Endpoint == e {
			visto = a.Visto
		}
	}
	if _, resta := visto["R16"]; resta {
		t.Error("la memoria di R16 è rimasta")
	}
	if v, c := visto["S2"]; !c || v.Stato != trenord.Critico {
		t.Errorf("la memoria di S2 è %+v, attesa critica", visto["S2"])
	}
}

// Il visto non si prende da chi si registra: è memoria del servizio, e un
// client che lo mandasse potrebbe farsi raccontare di nuovo cose già dette —
// o, peggio, dichiarare di sapere cose che non sa.
func TestIlClientNonPuoScrivereLaMemoria(t *testing.T) {
	ab, _ := ApriAbbonati("")
	e := "https://push.example/uno"
	if err := ab.Registra(Abbonamento{
		Sottoscrizione: webpush.Subscription{Endpoint: e, Keys: chiaviFinte(t)},
		Linee:          []string{"S2"},
		Visto:          map[string]Visto{"S2": {Stato: trenord.Grave}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, a := range ab.Tutti() {
		if len(a.Visto) != 0 {
			t.Errorf("il client ha scritto la memoria: %+v", a.Visto)
		}
	}
}

// Le fasce arrivano da fuori: quelle che non si leggono vanno rifiutate alla
// porta, non tenute in uno schedario dove non terranno in ascolto nessuno.
func TestAbbonamentoConFasciaRottaRifiutato(t *testing.T) {
	ab, _ := ApriAbbonati("")
	casi := map[string]Abbonamento{
		"orario impossibile": {Fasce: []Fascia{{Da: "25:00", A: "09:00"}}},
		"estremi uguali":     {Fasce: []Fascia{{Da: "07:00", A: "07:00"}}},
		"giorno inventato":   {Fasce: []Fascia{{Giorni: []int{9}, Da: "07:00", A: "09:00"}}},
		"fuso inventato":     {Zona: "Europe/Atlantide"},
		"troppe fasce":       {Fasce: make([]Fascia, MaxFasce+1)},
	}
	for nome, caso := range casi {
		t.Run(nome, func(t *testing.T) {
			caso.Sottoscrizione = webpush.Subscription{
				Endpoint: "https://push.example/uno", Keys: chiaviFinte(t),
			}
			caso.Linee = []string{"S2"}
			if err := ab.Registra(caso); err == nil {
				t.Error("accettato")
			}
		})
	}
}

// Un fuso vero invece si accetta, e le fasce si leggono su quel quadrante.
func TestLeFasceSiLeggonoNelFusoDelTelefono(t *testing.T) {
	ab := Abbonamento{Fasce: []Fascia{mattina}, Zona: "Europe/Rome"}
	// Le 6 UTC d'estate a Roma sono le 8: dentro la fascia del mattino.
	if !ab.InAscolto(oreDi(8).UTC()) {
		t.Error("le 8 italiane non sono in ascolto")
	}
	// Lo stesso istante letto a Londra è un'ora prima: fuori.
	londra := Abbonamento{Fasce: []Fascia{mattina}, Zona: "Europe/London"}
	if londra.InAscolto(oreDi(7).UTC()) {
		t.Error("le 7 italiane, che a Londra sono le 6, sono in ascolto")
	}
}

// Il contratto con il telefono, per come lo scrive il telefono.
//
// I nomi dei campi JSON sono l'unico punto in cui il client e il servizio
// possono non capirsi in silenzio: sbagliandone uno, le fasce arriverebbero
// vuote e le notifiche tornerebbero ad arrivare tutto il giorno senza che
// niente si lamenti. Questo corpo è copiato da quello che manda l'app.
func TestLeFasceArrivanoDalTelefono(t *testing.T) {
	ab, _ := ApriAbbonati("")
	svc := Nuovo(nil).ConNotifiche(ab, nil)

	corpo := `{
	  "subscription": {"endpoint": "https://push.example/uno",
	                   "keys": {"auth": "YXV0aA", "p256dh": "cDI1NmRo"}},
	  "lines": ["S2"],
	  "windows": [{"days": [1,2,3,4,5], "from": "07:00", "to": "09:00"},
	              {"days": [1,2,3,4,5], "from": "17:00", "to": "19:00"}],
	  "timezone": "Europe/Rome"
	}`
	r := httptest.NewRequest(http.MethodPost, "/push/abbonamenti", strings.NewReader(corpo))
	w := httptest.NewRecorder()
	svc.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("stato = %d, atteso 204: %s", w.Code, w.Body)
	}

	tutti := ab.Tutti()
	if len(tutti) != 1 {
		t.Fatalf("abbonamenti = %d, atteso 1", len(tutti))
	}
	got := tutti[0]
	if got.Zona != "Europe/Rome" {
		t.Errorf("fuso = %q", got.Zona)
	}
	if len(got.Fasce) != 2 {
		t.Fatalf("fasce = %d, attese 2: %+v", len(got.Fasce), got.Fasce)
	}
	if got.Fasce[0].Da != "07:00" || got.Fasce[0].A != "09:00" {
		t.Errorf("prima fascia = %+v", got.Fasce[0])
	}
	if !slices.Equal(got.Fasce[0].Giorni, []int{1, 2, 3, 4, 5}) {
		t.Errorf("giorni = %v", got.Fasce[0].Giorni)
	}

	// E la prova che serve davvero: quelle fasce tengono in ascolto alle 8 e
	// non alle 15.
	if !got.InAscolto(oreDi(8)) {
		t.Error("alle 8 non è in ascolto")
	}
	if got.InAscolto(oreDi(15)) {
		t.Error("alle 15 è in ascolto")
	}
	if !got.InAscolto(oreDi(18)) {
		t.Error("alle 18 non è in ascolto")
	}
}

// Una fascia che non si legge si rifiuta alla porta, con un 400: accettarla
// vorrebbe dire tenerla in uno schedario dove non terrà in ascolto nessuno.
func TestFasciaRottaRifiutataDallHandler(t *testing.T) {
	ab, _ := ApriAbbonati("")
	svc := Nuovo(nil).ConNotifiche(ab, nil)
	corpo := `{"subscription": {"endpoint": "https://push.example/uno",
	           "keys": {"auth": "YXV0aA", "p256dh": "cDI1NmRo"}},
	           "lines": ["S2"], "windows": [{"from": "25:00", "to": "09:00"}]}`
	w := httptest.NewRecorder()
	svc.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/push/abbonamenti", strings.NewReader(corpo)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("stato = %d, atteso 400", w.Code)
	}
	if ab.Quanti() != 0 {
		t.Error("abbonamento registrato comunque")
	}
}

// avvisoSciopero è la comunicazione vera del primo settembre, accorciata.
func avvisoSciopero() trenord.Avviso {
	return trenord.Avviso{
		Testo:    "I sindacati CUB TRASPORTI e SGB hanno indetto uno sciopero nazionale dalle ore 21:18 del 7 settembre.",
		Sezione:  trenord.SezioneAvvisi,
		Sciopero: true,
	}
}

// avvisoProgrammato sta nella stessa sezione dello sciopero e non è uno
// sciopero: è la variazione d'orario che resta pubblicata per settimane, e per
// cui nessuno vuole essere svegliato.
func avvisoProgrammato() trenord.Avviso {
	return trenord.Avviso{
		Testo:   "Dal 24 agosto al 13 settembre i seguenti treni subiscono variazioni.",
		Sezione: trenord.SezioneAvvisi,
	}
}

// Uno sciopero notifica, anche stando fra gli avvisi programmati — che è dove
// sta. È l'eccezione al silenzio su quella sezione, e ha una ragione: arriva
// giorni prima e cambia la giornata più di qualunque ritardo.
func TestLoScioperoNotifica(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)
	ab, _ := ApriAbbonati("")
	dove := abbonaA(t, ab, srv.URL, "lavoratore", mattina)
	n := notificatoreDiProva(t, ab, srv.Client())

	reg := registroCon(t, "S2", trenord.Regolare)
	n.Riconcilia(context.Background(), reg, oreDi(8))

	metti(reg, "S2", trenord.Regolare, 2, avvisoSciopero())
	n.Riconcilia(context.Background(), reg, oreDi(8))

	mu.Lock()
	defer mu.Unlock()
	if q := quante(viste, dove); q != 1 {
		t.Fatalf("notifiche = %d, attesa 1", q)
	}
}

// Gli altri avvisi programmati continuano a tacere: se notificassero, ogni
// riavvio del servizio annuncerebbe i lavori di agosto.
func TestGliAvvisiProgrammatiRestanoZitti(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)
	ab, _ := ApriAbbonati("")
	dove := abbonaA(t, ab, srv.URL, "lavoratore", mattina)
	n := notificatoreDiProva(t, ab, srv.Client())

	reg := registroCon(t, "S2", trenord.Regolare)
	n.Riconcilia(context.Background(), reg, oreDi(8))

	metti(reg, "S2", trenord.Regolare, 2, avvisoProgrammato())
	n.Riconcilia(context.Background(), reg, oreDi(8))

	mu.Lock()
	defer mu.Unlock()
	if q := quante(viste, dove); q != 0 {
		t.Fatalf("notifiche = %d, attesa nessuna", q)
	}
}

// Sciopero e guasto insieme fanno due notifiche, non una: sono due cose da
// sapere entrambe, e sulla schermata di blocco non devono sostituirsi a
// vicenda — per questo hanno tag diversi.
func TestScioperoEGuastoSonoDueNotifiche(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)
	ab, _ := ApriAbbonati("")
	dove := abbonaA(t, ab, srv.URL, "lavoratore", mattina)
	n := notificatoreDiProva(t, ab, srv.Client())

	reg := registroCon(t, "S2", trenord.Regolare)
	n.Riconcilia(context.Background(), reg, oreDi(8))

	// La linea peggiora e insieme compare lo sciopero.
	metti(reg, "S2", trenord.Critico, 2, avvisoSciopero(),
		trenord.Avviso{Testo: "Guasto agli impianti a Seveso.", Sezione: trenord.SezioneCircolazione})
	n.Riconcilia(context.Background(), reg, oreDi(8))

	mu.Lock()
	defer mu.Unlock()
	if q := quante(viste, dove); q != 2 {
		t.Fatalf("notifiche = %d, attese 2", q)
	}
}

// E una volta detto, non si ripete: al giro dopo lo sciopero è lo stesso.
func TestLoScioperoNonSiRipete(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)
	ab, _ := ApriAbbonati("")
	dove := abbonaA(t, ab, srv.URL, "lavoratore", mattina)
	n := notificatoreDiProva(t, ab, srv.Client())

	reg := registroCon(t, "S2", trenord.Regolare)
	n.Riconcilia(context.Background(), reg, oreDi(8))
	metti(reg, "S2", trenord.Regolare, 2, avvisoSciopero())
	n.Riconcilia(context.Background(), reg, oreDi(8))
	metti(reg, "S2", trenord.Regolare, 3, avvisoSciopero())
	n.Riconcilia(context.Background(), reg, oreDi(8))

	mu.Lock()
	defer mu.Unlock()
	if q := quante(viste, dove); q != 1 {
		t.Fatalf("notifiche = %d, attesa 1: lo sciopero è stato riannunciato", q)
	}
}
