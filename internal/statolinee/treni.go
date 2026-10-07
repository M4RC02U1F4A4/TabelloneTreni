package statolinee

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"time"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/rfi"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/stations"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/vt"
)

// IntervalloTreni è il ritmo con cui si rilegge un treno seguito.
//
// Un minuto e non cinque come le linee: i bollini li muove una persona dalla
// sala operativa, un treno si muove da sé, e chi aspetta in stazione con un
// regionale in ritardo guarda il telefono molto più spesso di così. È anche la
// stessa cadenza con cui il telefono rilegge il treno quando l'app è aperta,
// il che rende le due viste indistinguibili — che è il punto.
const IntervalloTreni = time.Minute

// MaxTreniPerGiro è l'ultima difesa sul traffico verso ViaggiaTreno. Gemello
// di MaxAvvisiPerGiro e messo per lo stesso motivo: prima di averne bisogno.
//
// È un tetto sull'unione deduplicata, quindi non sui telefoni: cento persone
// sui quaranta treni più seguiti ci stanno dentro tutte.
const MaxTreniPerGiro = 40

// DurataSeguito è la rete di sicurezza per i viaggi di cui ViaggiaTreno non
// dirà mai più niente — si dimentica i treni di ieri, e un treno che parte
// alle 23:50 arriva il giorno dopo. È la stessa soglia che il telefono applica
// al proprio elenco.
const DurataSeguito = 36 * time.Hour

// SorgenteTreni è da dove arriva il viaggio di un treno. Interfaccia per lo
// stesso motivo di Sorgente: poter provare il ciclo senza rete.
type SorgenteTreni interface {
	Andamento(ctx context.Context, codOrigine, numero string, data int64) (*vt.Andamento, error)
}

// ConTreni accende l'inseguimento dei treni seguiti. Senza, il servizio fa
// tutto il resto come prima: è la stessa forma di ConNotifiche, e per la
// stessa ragione — una cosa in più che può mancare senza portarsi via le
// altre.
func (s *Servizio) ConTreni(f SorgenteTreni) *Servizio {
	s.treni = f
	return s
}

// OsservaTreni legge i treni seguiti e avvisa chi li aspetta, finché il
// contesto non finisce.
//
// Gira per conto suo e non dentro Osserva perché ha un ritmo diverso, e un
// ritmo diverso è un secondo ticker: infilarlo nell'altro avrebbe voluto dire
// o rileggere i treni ogni cinque minuti o Trenord ogni minuto, e sono
// entrambe la risposta sbagliata.
func (s *Servizio) OsservaTreni(ctx context.Context) {
	if s.treni == nil || s.abbonati == nil || s.notificatore == nil {
		return
	}
	s.leggiTreni(ctx)
	t := time.NewTicker(IntervalloTreni)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.leggiTreni(ctx)
		}
	}
}

func (s *Servizio) leggiTreni(ctx context.Context) {
	adesso := s.orologio()
	seguiti := s.abbonati.TreniSeguiti(adesso)
	if len(seguiti) == 0 {
		return
	}
	if len(seguiti) > MaxTreniPerGiro {
		log.Printf("treni seguiti: %d, ne leggo %d", len(seguiti), MaxTreniPerGiro)
		seguiti = seguiti[:MaxTreniPerGiro]
	}

	// Le notifiche non stanno sotto il budget delle letture: sono una richiesta
	// di rete per destinatario, e un giro di letture andato lungo non deve
	// farle fallire tutte insieme con un contesto già scaduto. Ognuna ha il
	// proprio timeout, dentro manda. È la stessa separazione che il giro delle
	// linee fa con Riconcilia.
	letti := s.leggiViaggi(ctx, seguiti)
	tabelloni := s.leggiTabelloni(ctx, s.abbonati.PreavvisiDovuti(adesso))
	s.riconciliaTreni(context.WithoutCancel(ctx), letti, tabelloni, adesso)
}

// leggiViaggi chiede a ViaggiaTreno i treni di questo giro.
//
// Le letture di questo giro e basta: non c'è un registro che le tenga da un
// giro all'altro, e non serve. A differenza dei bollini, che un handler HTTP
// serve al telefono, un viaggio qui dentro vive il tempo di decidere chi
// avvisare. Una lettura fallita è semplicemente un treno assente dalla mappa,
// che il giro dopo si riprova — senza nessuno stato da invalidare.
//
// ponytail: letture in fila, una dopo l'altra. Con i treni che ci si aspetta —
// una manciata — è un secondo scarso; al tetto di quaranta, e con ViaggiaTreno
// che arriva agli otto secondi di ogni lettura, il budget scade e gli ultimi
// treni saltano il giro. Se dovesse capitare davvero, la strada è un pugno di letture in
// parallelo, non un budget più lungo: il giro dopo parte comunque fra un
// minuto.
func (s *Servizio) leggiViaggi(ctx context.Context, seguiti []TrenoSeguito) map[string]*vt.Andamento {
	ctx, annulla := context.WithTimeout(ctx, IntervalloTreni)
	defer annulla()

	letti := make(map[string]*vt.Andamento, len(seguiti))
	for _, t := range seguiti {
		// Gli otto secondi stanno qui e non nel client: il client ne concede
		// venti al viaggio, perché per la scheda sul telefono ViaggiaTreno è
		// l'unica lettura. In un giro in fila dentro un minuto, venti a treno
		// vorrebbero dire tre treni bloccati e nessuna notifica per gli altri.
		lettura, annullaLettura := context.WithTimeout(ctx, 8*time.Second)
		a, err := s.treni.Andamento(lettura, t.Origine, t.Numero, t.Data)
		annullaLettura()
		if err != nil {
			log.Printf("viaggio %s: %v", t.Chiave(), err)
			continue
		}
		// Corpo vuoto: ViaggiaTreno quel treno non lo traccia. Non è un errore
		// e non è una notizia — si tace e si riprova.
		if a == nil {
			continue
		}
		letti[t.Chiave()] = a
	}
	return letti
}

/*
riconciliaTreni racconta a ognuno quello che non sa ancora dei treni che
aspetta, e toglie dall'elenco quelli finiti.

	La regola è "una notifica a ogni rilevamento", quindi qui non si cerca cosa è
	cambiato nel mondo ma cosa è cambiato *rispetto a quello che questa persona
	ha già ricevuto* — la stessa forma di Riconcilia per le linee, e per la
	stessa ragione: due persone sullo stesso treno possono essere a due punti
	diversi della storia, perché lo hanno seguito in due momenti diversi.

	Le fasce non si guardano. Esistono per il ronzio di fondo di una linea che
	si segue per mesi; un treno lo si segue a mano adesso e dura due ore, e il
	segnalibro è già il consenso. Chi segue un treno alle 23 vuole saperlo alle
	23. Lo stesso per gli abituali: averlo salvato è il consenso, una volta
	per tutte.

	Il treno di oggi di un abituale apre con il preavviso, che prende il posto
	del primo giro muto: è la prima cosa che gli si dice, e da lì i
	rilevamenti si contano come per un segnalibro.
*/
func (s *Servizio) riconciliaTreni(ctx context.Context, letti map[string]*vt.Andamento, tabelloni map[int]*rfi.Board, adesso time.Time) {
	for _, ab := range s.abbonati.Tutti() {
		attese := ab.inAttesa(adesso)
		if len(attese) == 0 {
			continue
		}
		visto := map[string]VistoTreno{}
		for k, v := range ab.VistoTreni {
			visto[k] = v
		}
		var finiti []string
		cambiato := false

		for _, t := range attese {
			k := t.Chiave()
			// Scaduto: di questo viaggio non si saprà più niente, e tenerlo
			// nell'elenco vorrebbe dire interrogare ViaggiaTreno per sempre su
			// un treno di ieri.
			if adesso.Sub(time.UnixMilli(t.Data)) > DurataSeguito {
				finiti, cambiato = append(finiti, k), true
				continue
			}
			prima, gia := ab.VistoTreni[k]
			if prima.Finito {
				continue // il treno di oggi di un abituale, già arrivato
			}
			preavvisato := false
			if t.preavvisoDovuto(prima, adesso) {
				if b := tabelloni[t.Da]; b != nil {
					s.avvisa(ctx, ab, k, messaggioPreavviso(t, b))
					preavvisato = true
				}
			}

			a := letti[k]
			if a == nil {
				// Non letto in questo giro: si riprova al prossimo. Il
				// preavviso però è partito, e va ricordato.
				if preavvisato {
					prima.Preavviso = true
					visto[k], cambiato = prima, true
				}
				continue
			}

			salita := fermataDi(a, t.Da)
			adessoVisto := VistoTreno{
				Rilevamento:   rilevamento(a),
				Ritardo:       a.Ritardo,
				Provvedimento: a.ConProvvedimento,
			}
			if salita != nil {
				adessoVisto.Binario = salita.Binario()
			}
			adessoVisto.Preavviso = prima.Preavviso || preavvisato
			// Finito quando il treno è arrivato dove questa persona sale: da lì
			// in poi è a bordo, e quello che il treno fa dopo lo vede dal
			// finestrino. Arrivato al capolinea è la rete di sicurezza per la
			// fermata saltata, per chi sale al capolinea, e per la stazione che
			// il catalogo non sa tradurre.
			finito := a.Arrivato || (salita != nil && salita.Passata)
			adessoVisto.Finito = finito

			if !gia || preavvisato {
				// Primo giro su questo treno: si prende nota e si tace. Il
				// segnalibro si mette guardando la scheda del treno, e
				// notificare quello che si sta già leggendo sarebbe una
				// suoneria di benvenuto. Per il treno di oggi di un abituale
				// il benvenuto è il preavviso, appena partito: anche lì questo
				// giro prende nota e basta.
				//
				// Se la fermata di salita è già passata, questo primo giro è
				// anche l'ultimo: è chi segue un treno su cui è già salito, per
				// vedere quando arriva, e ha detto lui che da lì in poi non gli
				// interessa.
				visto[k], cambiato = adessoVisto, true
				if finito {
					finiti = append(finiti, k)
				}
				continue
			}
			if adessoVisto == prima {
				continue
			}

			s.avvisa(ctx, ab, k, messaggioTreno(a, t.TrenoSeguito, salita))
			visto[k], cambiato = adessoVisto, true
			if finito {
				finiti = append(finiti, k)
			}
		}
		if cambiato {
			s.abbonati.SegnaVistoTreni(ab.Sottoscrizione.Endpoint, visto, finiti, adesso)
		}
	}
}

func (s *Servizio) avvisa(ctx context.Context, ab Abbonamento, k string, m messaggio) {
	corpo, err := json.Marshal(m)
	if err != nil {
		return
	}
	// Una riga anche sull'invio riuscito, come per le linee: senza, "spedita"
	// e "spedita e non consegnata" sono indistinguibili dai log, e ogni
	// segnalazione ripartirebbe da zero.
	log.Printf("%s notifica a %s: %s", k, breve(ab.Sottoscrizione.Endpoint), m.Corpo)
	s.notificatore.manda(ctx, ab, corpo)
}

// rilevamento è l'ora dell'ultimo rilevamento in secondi, e zero finché il
// treno non è passato da nessun punto di controllo. Zero e non un errore: è
// lo stato normale di un treno non ancora partito, e deve poter stare in
// un'impronta senza farla sembrare cambiata a ogni giro.
func rilevamento(a *vt.Andamento) int64 {
	if a.Ora.IsZero() {
		return 0
	}
	return a.Ora.Unix()
}

// fermataDi ritrova, fra le fermate del viaggio, quella da cui sale chi segue
// il treno.
//
// La traduzione è la stessa che fa il tabellone sulle proprie rotte: l'id RFI
// della stazione dà i codici ViaggiaTreno con cui le fermate si chiamano. Nil
// quando il catalogo non conosce quella stazione o quando il treno lì non
// ferma — e allora valgono le sole reti di sicurezza, che è il verso giusto in
// cui sbagliare: meglio una notifica di troppo che un treno seguito per
// sempre.
func fermataDi(a *vt.Andamento, da int) *vt.Fermata {
	st := stations.Default.ByID(da)
	if st == nil {
		return nil
	}
	codici := st.CodiciVT()
	for i := range a.Fermate {
		if slices.Contains(codici, a.Fermate[i].Codice) {
			return &a.Fermate[i]
		}
	}
	return nil
}

// messaggioTreno scrive le due righe che si leggono sulla schermata di blocco:
// dov'è il treno, e cosa vuol dire per chi lo aspetta.
func messaggioTreno(a *vt.Andamento, t TrenoSeguito, salita *vt.Fermata) messaggio {
	titolo := a.Categoria + " " + a.Numero
	if a.Destinazione != "" {
		titolo += " → " + a.Destinazione
	}
	// Arrivato dove si sale, la notizia è una sola e questa è l'ultima
	// notifica del treno: dirla due volte — "visto a Garibaldi" e "è arrivato a
	// Garibaldi" — riempirebbe la riga con la stessa cosa scritta due modi.
	corpo := doveSta(a)
	if salita != nil && salita.Passata {
		corpo = eArrivato(salita, a.Ritardo)
	}
	if a.ConProvvedimento {
		// Non si dice *quale* provvedimento: il codice di ViaggiaTreno non è
		// documentato, e stampare "soppresso" per un valore mai visto sarebbe
		// inventarlo. Che ci sia qualcosa, però, va detto subito.
		corpo = "Provvedimento sul treno. " + corpo
	}
	if salita != nil && !salita.Passata {
		if s := perTe(salita); s != "" {
			corpo += " " + s
		}
	}
	return messaggio{
		Titolo: titolo,
		Corpo:  taglia(corpo, 180),
		// Un tag per treno: con una notifica a ogni rilevamento, la seconda
		// sostituisce la prima sulla schermata di blocco, perché è la seconda
		// quella vera.
		Tag: "treno-" + t.Chiave(),
		URL: fmt.Sprintf("./#/t/%s/%s/%d", t.Origine, t.Numero, t.Data),
	}
}

func doveSta(a *vt.Andamento) string {
	if a.Stazione == "" {
		// Nessun rilevamento: il treno non è ancora partito, oppure
		// ViaggiaTreno non l'ha ancora visto passare da nessuna parte. Dire
		// "visto a" senza un posto sarebbe una riga che non significa niente.
		return "Non ancora partito, " + ritardoScritto(a.Ritardo) + "."
	}
	return fmt.Sprintf("Visto a %s alle %s, %s.",
		a.Stazione, a.Ora.In(roma).Format("15:04"), ritardoScritto(a.Ritardo))
}

// perTe è la riga che riguarda chi legge: quando il treno arriva dove sale, e
// da che binario. Vuota quando la fermata non si è trovata — senza, ci sarebbe
// una frase su una stazione di cui non si sa niente.
func perTe(f *vt.Fermata) string {
	if f == nil {
		return ""
	}
	s := "A " + f.Nome
	if !f.Programmata.IsZero() {
		s += " alle " + f.Programmata.In(roma).Format("15:04")
	}
	if b := f.Binario(); b != "" {
		s += ", bin. " + b
	}
	return s + "."
}

// eArrivato è l'ultima notifica di un treno: quella per cui lo si seguiva.
func eArrivato(f *vt.Fermata, ritardo int) string {
	s := "È arrivato a " + f.Nome
	if !f.Effettiva.IsZero() {
		s += " alle " + f.Effettiva.In(roma).Format("15:04")
	}
	if f.Ritardo != 0 {
		// Il ritardo della fermata e non quello del treno: sono la stessa cosa
		// solo se il rilevamento è questo, e non sempre lo è.
		ritardo = f.Ritardo
	}
	s += ", " + ritardoScritto(ritardo)
	if b := f.Binario(); b != "" {
		s += ", bin. " + b
	}
	return s + "."
}

func ritardoScritto(m int) string {
	switch {
	case m > 0:
		return fmt.Sprintf("+%d min", m)
	case m < 0:
		return fmt.Sprintf("%d min", m)
	}
	return "in orario"
}
