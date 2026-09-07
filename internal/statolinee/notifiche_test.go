package statolinee

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/trenord"
)

// chiaviFinte fa quello che fa il browser quando si abbona: una coppia P-256 e
// un segreto di autenticazione. Servono vere, altrimenti la cifratura non ha
// niente su cui lavorare e il test proverebbe solo che la libreria esiste.
func chiaviFinte(t *testing.T) webpush.Keys {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	return webpush.Keys{
		P256dh: base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()),
		Auth:   base64.RawURLEncoding.EncodeToString(auth),
	}
}

type richiesta struct {
	percorso  string
	autorizza string
	codifica  string
	byte      int
}

// servizioPushFinto sta al posto del servizio push del browser e annota cosa
// gli arriva. Risponde con il codice che gli si dice.
func servizioPushFinto(t *testing.T, codice int) (*httptest.Server, *[]richiesta, *sync.Mutex) {
	var mu sync.Mutex
	var viste []richiesta
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 4096)
		n, _ := r.Body.Read(b)
		mu.Lock()
		viste = append(viste, richiesta{
			percorso:  r.URL.Path,
			autorizza: r.Header.Get("Authorization"),
			codifica:  r.Header.Get("Content-Encoding"),
			byte:      n,
		})
		mu.Unlock()
		w.WriteHeader(codice)
	}))
	t.Cleanup(srv.Close)
	return srv, &viste, &mu
}

func notificatoreDiProva(t *testing.T, ab *Abbonati, c *http.Client) *Notificatore {
	t.Helper()
	privata, pubblica, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	n := NuovoNotificatore(ab, pubblica, privata, "mailto:prova@example.com")
	n.HTTP = c
	return n
}

func cambio(codice string, da, a trenord.Stato) Cambio {
	return Cambio{Linea: trenord.Linea{Codice: codice, Nome: codice + " prova", Stato: a}, Prima: da}
}

// Il giro completo: chi segue la linea riceve un messaggio cifrato e firmato,
// chi non la segue non riceve niente.
func TestNotificaSoloAChiSegue(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)

	ab, _ := ApriAbbonati("")
	chiavi := chiaviFinte(t)
	if err := ab.Registra(Abbonamento{
		Sottoscrizione: webpush.Subscription{Endpoint: srv.URL + "/segue-s2", Keys: chiavi},
		Linee:          []string{"S2"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := ab.Registra(Abbonamento{
		Sottoscrizione: webpush.Subscription{Endpoint: srv.URL + "/segue-altro", Keys: chiaviFinte(t)},
		Linee:          []string{"R16"},
	}); err != nil {
		t.Fatal(err)
	}

	n := notificatoreDiProva(t, ab, srv.Client())
	c := cambio("S2", trenord.Regolare, trenord.Critico)
	n.Annuncia(context.Background(), Novita{Cambio: &c})

	mu.Lock()
	defer mu.Unlock()
	if len(*viste) != 1 {
		t.Fatalf("richieste = %d, attesa 1: %+v", len(*viste), *viste)
	}
	r := (*viste)[0]
	if r.percorso != "/segue-s2" {
		t.Errorf("recapitata a %q", r.percorso)
	}
	// Il corpo deve essere cifrato secondo RFC 8291 e la richiesta firmata
	// VAPID: senza, il servizio push vero la rifiuterebbe e non lo sapremmo.
	if r.codifica != "aes128gcm" {
		t.Errorf("Content-Encoding = %q, atteso aes128gcm", r.codifica)
	}
	if len(r.autorizza) < 10 || r.autorizza[:5] != "vapid" {
		t.Errorf("Authorization = %q, atteso un token vapid", r.autorizza)
	}
	if r.byte == 0 {
		t.Error("corpo vuoto: non e' stato cifrato niente")
	}
}

// 410 e' il servizio push che dice che quell'indirizzo non esiste piu': l'app
// e' stata disinstallata o il permesso revocato. Tenerlo vorrebbe dire
// riprovare a ogni cambio, per sempre.
func TestAbbonamentoScadutoVieneTolto(t *testing.T) {
	srv, _, _ := servizioPushFinto(t, http.StatusGone)

	ab, _ := ApriAbbonati("")
	ab.Registra(Abbonamento{
		Sottoscrizione: webpush.Subscription{Endpoint: srv.URL + "/morto", Keys: chiaviFinte(t)},
		Linee:          []string{"S2"},
	})

	n := notificatoreDiProva(t, ab, srv.Client())
	c := cambio("S2", trenord.Regolare, trenord.Critico)
	n.Annuncia(context.Background(), Novita{Cambio: &c})

	if ab.Quanti() != 0 {
		t.Fatalf("abbonamenti = %d, atteso nessuno", ab.Quanti())
	}
}

// Senza chiavi VAPID il notificatore non esiste e il servizio va avanti lo
// stesso: i bollini si vedono, sono le notifiche a mancare.
func TestSenzaChiaviNonNotifica(t *testing.T) {
	ab, _ := ApriAbbonati("")
	n := NuovoNotificatore(ab, "", "", "")
	if n != nil {
		t.Fatal("atteso nil senza chiavi")
	}
	// Deve reggere la chiamata su nil senza esplodere: e' il caso normale di
	// un'installazione senza notifiche configurate.
	c := cambio("S2", trenord.Regolare, trenord.Critico)
	n.Annuncia(context.Background(), Novita{Cambio: &c})
	if n.ChiavePubblica() != "" {
		t.Error("chiave pubblica non vuota")
	}
}

// Il testo dice il verso, non solo lo stato d'arrivo: sulla schermata di blocco
// si legge solo quella riga.
func TestTestoDelCambio(t *testing.T) {
	casi := []struct {
		c      Cambio
		atteso string
	}{
		{cambio("S2", trenord.Regolare, trenord.Critico), "Circolazione peggiorata: criticità"},
		{cambio("S2", trenord.Critico, trenord.Grave), "Circolazione peggiorata: gravi criticità"},
		{cambio("S2", trenord.Grave, trenord.Critico), "Circolazione migliorata: criticità"},
		{cambio("S2", trenord.Critico, trenord.Regolare), "Circolazione tornata regolare"},
	}
	for _, caso := range casi {
		if got := testoCambio(caso.c); got != caso.atteso {
			t.Errorf("%s -> %s: %q, atteso %q", caso.c.Prima, caso.c.Linea.Stato, got, caso.atteso)
		}
	}
}

// Le chiavi devono restare le stesse fra un riavvio e l'altro: se cambiano,
// tutti gli abbonamenti gia' presi diventano inservibili, e falliscono in
// silenzio a ogni invio.
func TestChiaviStabiliFraRiavvii(t *testing.T) {
	f := filepath.Join(t.TempDir(), "chiavi.json")

	pub1, priv1, err := ApriChiavi(f)
	if err != nil {
		t.Fatal(err)
	}
	if pub1 == "" || priv1 == "" {
		t.Fatal("chiavi vuote al primo avvio")
	}

	pub2, priv2, err := ApriChiavi(f)
	if err != nil {
		t.Fatal(err)
	}
	if pub1 != pub2 || priv1 != priv2 {
		t.Fatal("le chiavi sono cambiate al secondo avvio")
	}

	// La privata firma verso i servizi push: il file non deve essere leggibile
	// da chiunque passi sul volume.
	info, err := os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	if m := info.Mode().Perm(); m != 0o600 {
		t.Errorf("permessi = %o, attesi 600", m)
	}
}

// Senza percorso le chiavi si generano in memoria: serve a provare in locale,
// e deve funzionare, non fallire.
func TestChiaviInMemoria(t *testing.T) {
	pub, priv, err := ApriChiavi("")
	if err != nil {
		t.Fatal(err)
	}
	if pub == "" || priv == "" {
		t.Fatal("chiavi vuote")
	}
}

// Un file di chiavi rovinato e' meglio saperlo all'avvio che scoprirlo al
// primo invio fallito.
func TestChiaviRovinate(t *testing.T) {
	d := t.TempDir()
	for nome, contenuto := range map[string]string{
		"storto.json": "non-json",
		"a-meta.json": `{"public":"BLzN"}`,
	} {
		f := filepath.Join(d, nome)
		os.WriteFile(f, []byte(contenuto), 0o600)
		if _, _, err := ApriChiavi(f); err == nil {
			t.Errorf("%s: accettato, atteso un errore", nome)
		}
	}
}

// La notifica deve portare sulla linea, non sull'elenco intero: chi la tocca
// sta cercando quella riga, non le altre sessantaquattro.
func TestNotificaPortaSullaLinea(t *testing.T) {
	for _, codice := range []string{"S2", "RE_13", "R16"} {
		got := destinazione(codice)
		atteso := "./#/linee/" + codice
		if got != atteso {
			t.Errorf("%s: %q, atteso %q", codice, got, atteso)
		}
	}
}

// Il bollino che si muove e la comunicazione che lo spiega arrivano insieme:
// devono essere una notifica sola, con dentro il perche'. Due notifiche per lo
// stesso guasto sono il modo piu' rapido per farle spegnere.
func TestCambioEAvvisoFannoUnaNotificaSola(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)

	ab, _ := ApriAbbonati("")
	ab.Registra(Abbonamento{
		Sottoscrizione: webpush.Subscription{Endpoint: srv.URL + "/uno", Keys: chiaviFinte(t)},
		Linee:          []string{"S2"},
	})
	n := notificatoreDiProva(t, ab, srv.Client())

	c := cambio("S2", trenord.Regolare, trenord.Critico)
	n.Annuncia(context.Background(), Novita{
		Cambio: &c,
		Avvisi: []trenord.Avviso{{Testo: "Guasto agli impianti a Seveso."}},
	})

	mu.Lock()
	defer mu.Unlock()
	if len(*viste) != 1 {
		t.Fatalf("richieste = %d, attesa 1", len(*viste))
	}
}

// Senza niente di nuovo non parte niente: e' il caso di gran lunga piu'
// frequente, una lettura ogni cinque minuti in cui non e' successo nulla.
func TestNienteDaDireNienteNotifica(t *testing.T) {
	srv, viste, mu := servizioPushFinto(t, http.StatusCreated)
	ab, _ := ApriAbbonati("")
	ab.Registra(Abbonamento{
		Sottoscrizione: webpush.Subscription{Endpoint: srv.URL + "/uno", Keys: chiaviFinte(t)},
		Linee:          []string{"S2"},
	})
	notificatoreDiProva(t, ab, srv.Client()).Annuncia(context.Background(), Novita{})

	mu.Lock()
	defer mu.Unlock()
	if len(*viste) != 0 {
		t.Fatalf("richieste = %d, attesa nessuna", len(*viste))
	}
}
