package statolinee

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/trenord"
)

// Notificatore manda le notifiche a chi segue una linea che ha cambiato
// bollino.
type Notificatore struct {
	abbonati *Abbonati
	pubblica string
	privata  string
	soggetto string
	// HTTP sostituisce il client con cui si parla al servizio push. Serve ai
	// test, che mettono un servizio finto al posto di quello vero; a nil vale
	// il client di default della libreria.
	HTTP webpush.HTTPClient
}

// NuovoNotificatore restituisce nil se le chiavi VAPID non ci sono: senza non
// si puo' spedire niente, e il servizio deve continuare a funzionare lo stesso
// — i bollini si vedono comunque, sono le notifiche a mancare.
func NuovoNotificatore(ab *Abbonati, pubblica, privata, soggetto string) *Notificatore {
	if pubblica == "" || privata == "" {
		return nil
	}
	if soggetto == "" {
		soggetto = "https://github.com/M4RC02U1F4A4/TabelloneTreni"
	}
	return &Notificatore{abbonati: ab, pubblica: pubblica, privata: privata, soggetto: soggetto}
}

func (n *Notificatore) ChiavePubblica() string {
	if n == nil {
		return ""
	}
	return n.pubblica
}

// messaggio è quello che arriva al service worker.
type messaggio struct {
	Titolo string `json:"titolo"`
	Corpo  string `json:"corpo"`
	URL    string `json:"url"`
	// Tag fa sì che due notizie sulla stessa linea non si impilino: la seconda
	// sostituisce la prima, perché è la seconda quella vera.
	Tag string `json:"tag"`
}

// Annuncia manda a chi segue la linea quello che è cambiato.
//
// Il bollino che si muove e una comunicazione nuova sono due notizie diverse e
// vanno tutte e due, ma quando arrivano insieme è una notifica sola: sono la
// stessa cosa vista da due lati, e mandarne due per lo stesso guasto è il modo
// più rapido per far spegnere le notifiche.
func (n *Notificatore) Annuncia(ctx context.Context, v Novita) {
	if n == nil || (v.Cambio == nil && len(v.Avvisi) == 0) {
		return
	}
	codice, nome := v.codiceNome()
	destinatari := n.abbonati.PerLinea(codice)
	if len(destinatari) == 0 {
		return
	}

	m := messaggio{
		Titolo: nome,
		URL:    destinazione(codice),
		Tag:    "linea-" + codice,
	}
	switch {
	case v.Cambio != nil && len(v.Avvisi) > 0:
		// Il cambio dice cosa è successo, l'avviso perché: insieme sono la
		// notifica che serve davvero.
		m.Corpo = testoCambio(*v.Cambio) + " · " + taglia(v.Avvisi[0].Testo, 150)
	case v.Cambio != nil:
		m.Corpo = testoCambio(*v.Cambio)
	default:
		m.Corpo = taglia(v.Avvisi[0].Testo, 180)
	}

	corpo, err := json.Marshal(m)
	if err != nil {
		return
	}
	for _, ab := range destinatari {
		n.manda(ctx, ab, corpo)
	}
}

func (v Novita) codiceNome() (string, string) {
	if v.Cambio != nil {
		return v.Cambio.Linea.Codice, v.Cambio.Linea.Nome
	}
	return v.codice, v.nome
}

// destinazione porta dritto sulla linea invece che sull'elenco: chi tocca la
// notifica sta cercando quella riga, non le altre sessantaquattro.
func destinazione(codice string) string {
	return "./#/linee/" + url.PathEscape(codice)
}

// testoCambio dice il verso, non solo lo stato di arrivo: "torna regolare" e
// "in criticità" sono due notizie diverse, e chi legge la notifica sulla
// schermata di blocco vede solo questa riga.
func testoCambio(c Cambio) string {
	switch {
	case c.Linea.Stato == trenord.Regolare:
		return "Circolazione tornata regolare"
	case c.Linea.Stato > c.Prima:
		return "Circolazione peggiorata: " + etichetta(c.Linea.Stato)
	default:
		return "Circolazione migliorata: " + etichetta(c.Linea.Stato)
	}
}

func etichetta(s trenord.Stato) string {
	switch s {
	case trenord.Critico:
		return "criticità"
	case trenord.Grave:
		return "gravi criticità"
	}
	return "regolare"
}

func (n *Notificatore) manda(ctx context.Context, ab Abbonamento, corpo []byte) {
	ctx, annulla := context.WithTimeout(ctx, 15*time.Second)
	defer annulla()

	resp, err := webpush.SendNotificationWithContext(ctx, corpo, &ab.Sottoscrizione, &webpush.Options{
		HTTPClient:      n.HTTP,
		Subscriber:      n.soggetto,
		VAPIDPublicKey:  n.pubblica,
		VAPIDPrivateKey: n.privata,
		TTL:             int((30 * time.Minute).Seconds()),
		Urgency:         webpush.UrgencyHigh,
	})
	if err != nil {
		log.Printf("notifica non spedita: %v", err)
		return
	}
	defer resp.Body.Close()

	// 404 e 410 sono il servizio push che dice che quell'indirizzo non esiste
	// più: l'app è stata disinstallata o il permesso revocato. Insistere a ogni
	// cambio sarebbe traffico verso un telefono che non c'è.
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		log.Printf("abbonamento scaduto, lo tolgo")
		n.abbonati.Dimentica(ab.Sottoscrizione.Endpoint)
		return
	}
	if resp.StatusCode >= 300 {
		log.Printf("notifica rifiutata: %s", resp.Status)
	}
}

// chiaviVAPID è l'identità con cui il servizio si presenta ai servizi push.
type chiaviVAPID struct {
	Pubblica string `json:"public"`
	Privata  string `json:"private"`
}

// ApriChiavi carica le chiavi VAPID, generandole al primo avvio.
//
// Stanno sul volume accanto agli abbonamenti, e non in un segreto da creare a
// mano, perché le due cose vivono e muoiono insieme: cambiare le chiavi rende
// inservibili tutti gli abbonamenti presi, e perdere il volume li perde
// comunque. Tenerle separate creerebbe l'unico caso davvero brutto — chiavi
// nuove e abbonamenti vecchi — che è anche quello che nessuno noterebbe,
// perché fallisce in silenzio a ogni invio.
//
// Un percorso vuoto le genera in memoria: va bene per provare in locale, dove
// perderle a ogni riavvio non costa niente.
func ApriChiavi(percorso string) (pubblica, privata string, err error) {
	if percorso == "" {
		privata, pubblica, err = webpush.GenerateVAPIDKeys()
		return pubblica, privata, err
	}
	b, err := os.ReadFile(percorso)
	if err == nil {
		var c chiaviVAPID
		if err := json.Unmarshal(b, &c); err != nil {
			return "", "", fmt.Errorf("%s: %w", percorso, err)
		}
		if c.Pubblica == "" || c.Privata == "" {
			return "", "", fmt.Errorf("%s: chiavi incomplete", percorso)
		}
		return c.Pubblica, c.Privata, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}

	privata, pubblica, err = webpush.GenerateVAPIDKeys()
	if err != nil {
		return "", "", err
	}
	dati, err := json.Marshal(chiaviVAPID{Pubblica: pubblica, Privata: privata})
	if err != nil {
		return "", "", err
	}
	// 0600: la chiave privata è quella con cui si firma verso i servizi push.
	if err := scriviAtomico(percorso, dati, 0o600); err != nil {
		return "", "", err
	}
	return pubblica, privata, nil
}

// taglia accorcia il testo per la schermata di blocco, dove oltre un paio di
// righe non si legge comunque e il resto lo nasconde il sistema. Si taglia su
// uno spazio, per non mozzare una parola a metà.
func taglia(s string, max int) string {
	if len(s) <= max {
		return s
	}
	t := s[:max]
	if i := strings.LastIndex(t, " "); i > max/2 {
		t = t[:i]
	}
	return t + "…"
}
