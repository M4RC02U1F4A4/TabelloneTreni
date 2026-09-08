package statolinee

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
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

/*
Riconcilia racconta a ognuno quello che non sa ancora, ma solo mentre è in

	ascolto.

	Prende il posto del vecchio "annuncia il cambio a tutti nell'istante in cui
	succede", che con le fasce non poteva funzionare: il cambio si consumava una
	volta per tutti, e chi in quel momento non ascoltava non l'avrebbe saputo
	mai. Qui non c'è nessuna coda di notifiche rimandate — girando ogni pochi
	minuti, il servizio sa già com'è la linea *adesso*, e la domanda giusta non è
	"cos'è cambiato" ma "cosa non ti ho ancora detto".

	Da cui, gratis, la regola che serviva: un guasto comparso alle 6 e ancora lì
	alle 7 viene raccontato alle 7, perché alle 7 è ancora diverso da quello che
	sai. Uno comparso alle 6 e rientrato alle 6 e mezza, alle 7 non è più diverso
	da niente, e non arriva — che è esattamente il ronzio che le fasce dovevano
	togliere.

	Fuori dalla fascia non si tocca il visto: è quello che tiene la notizia in
	sospeso invece di consumarla in silenzio.
*/
func (n *Notificatore) Riconcilia(ctx context.Context, r *Registro, adesso time.Time) {
	if n == nil {
		return
	}
	for _, ab := range n.abbonati.Tutti() {
		inAscolto := ab.InAscolto(adesso)
		visto, cambiato := maps.Clone(ab.Visto), false
		if visto == nil {
			visto = map[string]Visto{}
		}
		for _, codice := range ab.Linee {
			stato, noto := r.StatoNoto(codice)
			if !noto {
				// Della linea non si è ancora letto il dettaglio, che è la sola
				// fonte con un orario: senza, non c'è niente da raccontare.
				continue
			}
			// Due cose diverse, tenute separate perché sono due notizie
			// diverse. La circolazione è quello che sta succedendo adesso; lo
			// sciopero è programmato, arriva giorni prima, e cambia la
			// giornata più di qualunque ritardo. Gli altri avvisi programmati —
			// variazioni d'orario, lavori, i PDF — restano fuori: stanno
			// pubblicati per settimane e non giustificano una vibrazione.
			tutti := r.AvvisiDi(codice)
			avvisi := diCircolazione(tutti)
			scioperi := diSciopero(tutti)
			// Il visto tiene le impronte di tutto ciò su cui si notifica, in un
			// insieme solo: serve a sapere cosa è già stato detto, non da quale
			// dei due passaggi.
			adessoVisto := Visto{
				Stato:  stato,
				Avvisi: append(impronteDi(avvisi), impronteDi(scioperi)...),
			}

			prima, gia := ab.Visto[codice]
			if !gia {
				// Prima volta su questa linea per questa persona: si prende
				// nota e si tace. Chi accende una campanella ha davanti lo
				// stato attuale — è il motivo per cui l'ha accesa — e aprirgli
				// una notifica per quello che sta già leggendo sarebbe una
				// suoneria di benvenuto.
				//
				// Questo si fa anche a fascia chiusa, ed è il punto delicato:
				// il primo contatto è il punto di partenza della storia, non
				// una notizia da rimandare. Rimandandolo, la prima mattina
				// utile si perderebbe — il guasto comparso alle 6 arriverebbe
				// alle 7 come punto di partenza invece che come novità, cioè in
				// silenzio, che è esattamente il caso per cui le fasce
				// esistono.
				visto[codice], cambiato = adessoVisto, true
				continue
			}
			freschi := nuoviAvvisi(avvisi, prima.Avvisi)
			nuoviScioperi := nuoviAvvisi(scioperi, prima.Avvisi)
			if stato == prima.Stato && len(freschi) == 0 && len(nuoviScioperi) == 0 {
				continue
			}
			// Fuori dalla fascia non si parla. Il visto resta indietro di
			// proposito: è quello che tiene la notizia in sospeso invece di
			// consumarla mentre nessuno ascolta.
			//
			// Si scrive nel log solo qui, dove qualcosa è stato davvero
			// trattenuto: una riga a ogni giro per ogni abbonato sarebbe
			// rumore, e questa invece è la riga che serve quando qualcuno dice
			// "non mi è arrivato niente".
			if !inAscolto {
				log.Printf("%s rimandato per %s: fuori fascia, da lui sono le %s (%s)",
					codice, breve(ab.Sottoscrizione.Endpoint),
					adesso.In(ab.fuso()).Format("Mon 15:04"), ab.fasceScritte())
				continue
			}
			// Il messaggio della circolazione si costruisce solo se c'è
			// circolazione di cui parlare. Costruirlo comunque significava
			// leggere `freschi[0]` con `freschi` vuoto, che è quello che
			// succede quando la sola novità è uno sciopero.
			if stato != prima.Stato || len(freschi) > 0 {
				m := messaggio{Titolo: r.NomeDi(codice), URL: destinazione(codice), Tag: "linea-" + codice}
				switch {
				case stato != prima.Stato && len(freschi) > 0:
					// Il cambio dice cosa è successo, l'avviso perché: insieme
					// sono la notifica che serve davvero.
					m.Corpo = testoCambio(Cambio{
						Linea: trenord.Linea{Codice: codice, Stato: stato}, Prima: prima.Stato,
					}) + " · " + taglia(freschi[0].Testo, 150)
				case stato != prima.Stato:
					m.Corpo = testoCambio(Cambio{
						Linea: trenord.Linea{Codice: codice, Stato: stato}, Prima: prima.Stato,
					})
				default:
					m.Corpo = taglia(freschi[0].Testo, 180)
				}
				if corpo, err := json.Marshal(m); err == nil {
					// Una riga anche sull'invio riuscito. Senza, "spedita" e
					// "spedita e non consegnata" sono indistinguibili dai log,
					// e ogni segnalazione riparte da zero.
					log.Printf("%s notifica a %s: %s", codice, breve(ab.Sottoscrizione.Endpoint), m.Corpo)
					n.manda(ctx, ab, corpo)
				}
			}
			// Lo sciopero va per conto suo, con un tag suo: non deve sostituire
			// sulla schermata di blocco la notizia di un guasto in corso, né
			// esserne sostituito. Sono due cose da sapere entrambe.
			if len(nuoviScioperi) > 0 {
				sc := messaggio{
					Titolo: "Sciopero · " + r.NomeDi(codice),
					URL:    destinazione(codice),
					Tag:    "sciopero-" + codice,
					Corpo:  taglia(nuoviScioperi[0].Testo, 180),
				}
				if corpo, err := json.Marshal(sc); err == nil {
					log.Printf("%s sciopero a %s: %.60s", codice, breve(ab.Sottoscrizione.Endpoint), sc.Corpo)
					n.manda(ctx, ab, corpo)
				}
			}
			// Il visto avanza comunque, riuscito l'invio o no. Non avanzare
			// vorrebbe dire riprovare la stessa notizia a ogni giro contro un
			// servizio push che l'ha già rifiutata; e quando il rifiuto è
			// definitivo l'abbonamento se ne va da sé — vedi manda.
			visto[codice], cambiato = adessoVisto, true
		}
		if cambiato {
			n.abbonati.SegnaVisto(ab.Sottoscrizione.Endpoint, visto)
		}
	}
}

// diCircolazione tiene le sole comunicazioni della sezione "STATO DELLA LINEA".
//
// Una sezione che non conosciamo la si tiene: è la parte di sorgente che
// potrebbe cambiare sotto, e tacere su qualcosa di sconosciuto è il modo di
// scoprirlo tardi.
func diCircolazione(avvisi []trenord.Avviso) []trenord.Avviso {
	var out []trenord.Avviso
	for _, a := range avvisi {
		// Uno sciopero ha la sua notifica: se ne comparisse uno nella sezione
		// della circolazione — non l'ho mai visto, ma la sorgente è di altri —
		// non deve arrivare due volte.
		if a.Sezione != trenord.SezioneAvvisi && !a.Sciopero {
			out = append(out, a)
		}
	}
	return out
}

// diSciopero tiene le sole comunicazioni che parlano di uno sciopero, da
// qualunque delle due liste vengano.
func diSciopero(avvisi []trenord.Avviso) []trenord.Avviso {
	var out []trenord.Avviso
	for _, a := range avvisi {
		if a.Sciopero {
			out = append(out, a)
		}
	}
	return out
}

func impronteDi(avvisi []trenord.Avviso) []string {
	if len(avvisi) == 0 {
		return nil
	}
	out := make([]string, 0, len(avvisi))
	for _, a := range avvisi {
		out = append(out, impronta(a.Testo))
	}
	return out
}

// nuoviAvvisi sono le comunicazioni di cui a questa persona non si è ancora
// detto niente.
func nuoviAvvisi(avvisi []trenord.Avviso, note []string) []trenord.Avviso {
	var freschi []trenord.Avviso
	for _, a := range avvisi {
		if !slices.Contains(note, impronta(a.Testo)) {
			freschi = append(freschi, a)
		}
	}
	return freschi
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
