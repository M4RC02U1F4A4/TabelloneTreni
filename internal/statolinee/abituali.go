package statolinee

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/board"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/rfi"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/stations"
)

const (
	// Un abituale si salva una volta e vale per mesi: andata e ritorno di
	// tutta la settimana sono una manciata, e venti lasciano spazio a chi
	// cambia treno a seconda del giorno.
	MaxAbitualiPerAbbonamento = 20

	// AnticipoPreavviso è quando, prima della partenza, il treno di oggi
	// comincia a esistere: è il momento del preavviso, ed è abbastanza presto
	// da cambiare programma — prendere quello prima, restare a casa — ma non
	// così presto che il tabellone non sappia ancora niente.
	AnticipoPreavviso = 10 * time.Minute

	// DurataAbituale è la rete di sicurezza del treno di oggi, come
	// DurataSeguito lo è del segnalibro: di solito esce prima, arrivando dove
	// si sale. Tre ore dopo l'orario un treno che non è ancora passato non è
	// più quello che si stava aspettando.
	DurataAbituale = 3 * time.Hour
)

// TrenoAbituale è un treno che si prende sempre: un segnalibro senza giorno,
// con i giorni della settimana in cui vale.
//
// Il numero e l'origine sono gli stessi ogni giorno, la stazione da cui si
// sale anche: cambia solo la data, che è l'unica coordinata che il servizio si
// calcola da sé. L'ora è quella prevista alla stazione di salita, letta sul
// tabellone da cui lo si è salvato, sul quadrante italiano — è quella di RFI.
type TrenoAbituale struct {
	Origine string `json:"origin"`
	Numero  string `json:"number"`
	Da      int    `json:"from"`
	Ora     string `json:"at"`
	// Con la convenzione di time.Weekday, come le fasce. Vuoto qui vuol dire
	// mai e non sempre: un abituale con tutte le caselle spente è uno messo in
	// pausa, non uno che vale tutti i giorni.
	Giorni []int `json:"days,omitempty"`
}

// DiOggi è il treno seguito che questo abituale diventa adesso, con l'ora in
// cui parte dalla stazione di salita. Falso fuori dalla finestra che va dal
// preavviso alla rete di sicurezza, e nei giorni non scelti.
//
// Si guardano anche ieri e domani perché la finestra può scavalcare la
// mezzanotte: il treno delle 23:30 si segue ancora all'una, e quello delle
// 00:05 si preavvisa alle 23:55 del giorno prima.
//
// ponytail: la data è quella del giorno in cui il treno passa dalla stazione
// di salita, mentre ViaggiaTreno vuole quella in cui parte dall'origine. Sono
// diverse solo per un treno che parte prima di mezzanotte e arriva dopo: lì il
// viaggio non si trova e resta il solo preavviso. Se servisse, la data giusta
// la dice l'andamento letto dal tabellone (`DataPartenza`).
func (t TrenoAbituale) DiOggi(adesso time.Time) (TrenoSeguito, time.Time, bool) {
	min, err := minutiDi(t.Ora)
	if err != nil {
		return TrenoSeguito{}, time.Time{}, false
	}
	oggi := adesso.In(roma)
	for _, d := range []int{-1, 0, 1} {
		giorno := time.Date(oggi.Year(), oggi.Month(), oggi.Day()+d, 0, 0, 0, 0, roma)
		if !slices.Contains(t.Giorni, int(giorno.Weekday())) {
			continue
		}
		// Ore e minuti dentro time.Date, non sommati alla mezzanotte: il
		// giorno del cambio d'ora dura ventitré o venticinque ore, e le 7:12
		// sarebbero le 6:12 o le 8:12.
		parte := time.Date(giorno.Year(), giorno.Month(), giorno.Day(), min/60, min%60, 0, 0, roma)
		if !adesso.Before(parte.Add(-AnticipoPreavviso)) && adesso.Before(parte.Add(DurataAbituale)) {
			return TrenoSeguito{Origine: t.Origine, Numero: t.Numero, Data: giorno.UnixMilli(), Da: t.Da}, parte, true
		}
	}
	return TrenoSeguito{}, time.Time{}, false
}

func (t TrenoAbituale) valida() error {
	if !codiceVT.MatchString(t.Origine) || !numeroTreno.MatchString(t.Numero) || t.Da <= 0 {
		return errors.New("treno abituale non valido")
	}
	if _, err := minutiDi(t.Ora); err != nil {
		return err
	}
	return validaGiorni(t.Giorni)
}

// attesa è un treno che qualcuno aspetta in questo giro: un segnalibro, o il
// treno di oggi di un abituale. Parte è zero per i segnalibri, che non hanno un
// orario di salita e quindi nemmeno un preavviso.
type attesa struct {
	TrenoSeguito
	parte time.Time
}

// inAttesa è quello che questo abbonato aspetta adesso: i segnalibri e gli
// abituali di oggi, una volta sola per viaggio. Un abituale messo anche fra i
// seguiti è lo stesso treno con la stessa chiave, e deve fare una scheda e una
// storia sola; dall'abituale prende l'orario, cioè il preavviso.
func (ab Abbonamento) inAttesa(adesso time.Time) []attesa {
	out := make([]attesa, 0, len(ab.Treni)+len(ab.Abituali))
	for _, t := range ab.Treni {
		out = append(out, attesa{TrenoSeguito: t})
	}
	for _, x := range ab.Abituali {
		t, parte, ok := x.DiOggi(adesso)
		if !ok {
			continue
		}
		if i := slices.IndexFunc(out, func(a attesa) bool { return a.Chiave() == t.Chiave() }); i >= 0 {
			out[i].parte = parte
			continue
		}
		out = append(out, attesa{TrenoSeguito: t, parte: parte})
	}
	return out
}

func (ab Abbonamento) chiaviInAttesa(adesso time.Time) []string {
	att := ab.inAttesa(adesso)
	out := make([]string, len(att))
	for i, a := range att {
		out[i] = a.Chiave()
	}
	return out
}

// preavvisoDovuto dice se a questo abbonato va ancora mandato il preavviso di
// questo treno. Dopo la partenza no: arriverebbe a treno passato.
func (a attesa) preavvisoDovuto(v VistoTreno, adesso time.Time) bool {
	return !a.parte.IsZero() && !v.Preavviso && !v.Finito && adesso.Before(a.parte)
}

// SorgenteTabelloni è il tabellone delle partenze, quello stesso che serve il
// telefono: RFI con sopra le misure di ViaggiaTreno. Interfaccia per poter
// provare il preavviso senza rete.
type SorgenteTabelloni interface {
	Get(ctx context.Context, from int, arrivals bool, to int) (*board.Result, error)
}

// ConTabelloni accende i preavvisi degli abituali. Senza, gli abituali
// diventano comunque i treni seguiti di oggi; manca solo la notifica prima
// della partenza.
func (s *Servizio) ConTabelloni(t SorgenteTabelloni) *Servizio {
	s.tabelloni = t
	return s
}

// leggiTabelloni prende le partenze delle stazioni da cui qualcuno sta per
// salire. Una lettura per stazione, qualunque sia il numero di treni e di
// persone, e il servizio del tabellone la tiene in cache trenta secondi.
//
// Una lettura fallita è una stazione assente dalla mappa: il preavviso si
// riprova al giro dopo, ed è diverso da un tabellone letto su cui il treno non
// c'è — che invece è una notizia.
func (s *Servizio) leggiTabelloni(ctx context.Context, stazioni []int) map[int]*rfi.Board {
	if s.tabelloni == nil || len(stazioni) == 0 {
		return nil
	}
	ctx, annulla := context.WithTimeout(ctx, IntervalloTreni/2)
	defer annulla()
	out := make(map[int]*rfi.Board, len(stazioni))
	for _, da := range stazioni {
		r, err := s.tabelloni.Get(ctx, da, false, 0)
		if err != nil {
			log.Printf("tabellone %d per il preavviso: %v", da, err)
			continue
		}
		out[da] = r.Board
	}
	return out
}

// rigaDi ritrova il treno sul tabellone. Prima quella con lo stesso orario:
// il treno sdoppiato compare due volte con lo stesso numero, e l'orario è
// l'unica cosa che le distingue.
func rigaDi(b *rfi.Board, numero, ora string) *rfi.Train {
	var prima *rfi.Train
	for i := range b.Trains {
		t := &b.Trains[i]
		if t.Number != numero {
			continue
		}
		if t.Time == ora {
			return t
		}
		if prima == nil {
			prima = t
		}
	}
	return prima
}

// messaggioPreavviso è la notifica dieci minuti prima: quello che dice la
// riga del tabellone, cioè quello che si leggerebbe aprendo l'app.
func messaggioPreavviso(a attesa, b *rfi.Board) messaggio {
	ora := a.parte.In(roma).Format("15:04")
	m := messaggio{
		Titolo: "Treno " + a.Numero + " delle " + ora,
		Tag:    "treno-" + a.Chiave(),
		URL:    fmt.Sprintf("./#/t/%s/%s/%d", a.Origine, a.Numero, a.Data),
	}
	r := rigaDi(b, a.Numero, ora)
	if r == nil {
		// Un festivo, un treno che oggi non circola, uno già sparito dal
		// tabellone: in tutti i casi è la cosa da sapere prima di uscire.
		nome := b.Station
		if st := stations.Default.ByID(a.Da); st != nil {
			nome = st.Name
		}
		m.Corpo = "Non compare sul tabellone di " + nome + "."
		return m
	}
	if r.Category != "" {
		m.Titolo = r.Category + " " + a.Numero + " delle " + ora
	}
	m.Corpo = taglia(rigaScritta(r), 180)
	return m
}

func rigaScritta(r *rfi.Train) string {
	if r.Cancelled {
		return "Soppresso."
	}
	var parti []string
	switch {
	case r.PlatformChanged && r.PlatformActual != "" && r.PlatformScheduled != "":
		parti = append(parti, fmt.Sprintf("Bin. %s, non il %s", r.PlatformActual, r.PlatformScheduled))
	case r.Platform != "":
		parti = append(parti, "Bin. "+r.Platform)
	default:
		parti = append(parti, "Binario non ancora assegnato")
	}
	// Lo stato vince sul numero: "RITARDO" è un ritardo annunciato e non
	// ancora quantificato, e lo zero accanto direbbe "in orario".
	ritardo := ritardoScritto(r.Delay)
	if r.Status != "" {
		ritardo = strings.ToLower(r.Status)
	}
	if r.LiveDelay != nil && *r.LiveDelay != r.Delay {
		ritardo += " (misurato " + ritardoScritto(*r.LiveDelay) + ")"
	}
	parti = append(parti, ritardo)
	s := strings.Join(parti, ", ") + "."
	if r.Notes != "" {
		s += " " + r.Notes
	}
	return s
}
