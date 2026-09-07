package statolinee

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	// Il database dei fusi dentro il binario. L'immagine è distroless static,
	// dove /usr/share/zoneinfo non è garantito: senza questo, LoadLocation
	// fallirebbe e le fasce si leggerebbero in UTC — d'estate due ore
	// sbagliate, cioè notifiche alle cinque del mattino. Costa mezzo mega e
	// toglie la dipendenza dall'immagine di base.
	_ "time/tzdata"
)

// MaxFasce è quante finestre può avere un abbonamento. Andata e ritorno di
// cinque giorni stanno in due fasce; otto lasciano spazio a una settimana
// irregolare senza aprire la porta a un elenco che non finisce.
const MaxFasce = 8

const (
	minutiGiorno    = 24 * 60
	minutiSettimana = 7 * minutiGiorno
)

// Fascia è una finestra della settimana in cui si vuole essere avvisati.
//
// Serve perché un guasto sulla linea con cui si va al lavoro è una notizia alle
// 7 e un ronzio alle 15. Fuori dalle fasce, però, non si tace e basta: si
// rimanda, e all'apertura della finestra si racconta quello che nel frattempo è
// rimasto vero. Il come sta in Notificatore.Riconcilia; qui c'è solo il
// calendario.
type Fascia struct {
	// I giorni in cui la fascia comincia, con la convenzione di time.Weekday:
	// domenica è 0. Vuoto vuol dire tutti, che è come si descrive "sempre"
	// senza dover elencare sette numeri.
	Giorni []int `json:"days,omitempty"`
	// Gli estremi, in ore e minuti locali: "07:00". Il minuto di apertura è
	// dentro, quello di chiusura è fuori — alle 9 in punto la finestra delle
	// 7-9 è chiusa, che è come si leggono gli orari di apertura di qualsiasi
	// cosa.
	Da string `json:"from"`
	A  string `json:"to"`
}

// contiene dice se quel momento cade nella fascia. adesso va passato già nel
// fuso di chi ha scritto la fascia: qui gli orari sono numeri sul quadrante di
// casa sua, non istanti.
func (f Fascia) contiene(adesso time.Time) bool {
	da, err := minutiDi(f.Da)
	if err != nil {
		return false
	}
	a, err := minutiDi(f.A)
	if err != nil {
		return false
	}
	// Una fascia che chiude prima di aprire scavalca la mezzanotte: 22:00-02:00
	// è una finestra sola di quattro ore, e i giorni sono quelli in cui apre.
	durata := (a - da + minutiGiorno) % minutiGiorno
	if durata == 0 {
		return false
	}
	ora := int(adesso.Weekday())*minutiGiorno + adesso.Hour()*60 + adesso.Minute()
	for _, g := range f.giorni() {
		apre := g*minutiGiorno + da
		// La distanza dall'apertura contata modulo la settimana: così la fascia
		// del sabato notte che sfocia nella domenica non perde il pezzo dopo la
		// mezzanotte, e nemmeno quella della domenica che sfocia nel lunedì.
		if (ora-apre+minutiSettimana)%minutiSettimana < durata {
			return true
		}
	}
	return false
}

func (f Fascia) giorni() []int {
	if len(f.Giorni) == 0 {
		return []int{0, 1, 2, 3, 4, 5, 6}
	}
	return f.Giorni
}

func (f Fascia) valida() error {
	if _, err := minutiDi(f.Da); err != nil {
		return fmt.Errorf("orario di inizio: %w", err)
	}
	if _, err := minutiDi(f.A); err != nil {
		return fmt.Errorf("orario di fine: %w", err)
	}
	// Estremi uguali: non è né una finestra vuota né una di ventiquattr'ore, è
	// una fascia che chi l'ha scritta non ha finito di scrivere.
	if f.Da == f.A {
		return errors.New("la fascia comincia e finisce alla stessa ora")
	}
	if len(f.Giorni) > 7 {
		return errors.New("troppi giorni")
	}
	for _, g := range f.Giorni {
		if g < 0 || g > 6 {
			return fmt.Errorf("giorno %d fuori dalla settimana", g)
		}
	}
	return nil
}

// minutiDi legge "07:00" e restituisce i minuti dalla mezzanotte.
func minutiDi(v string) (int, error) {
	o, m, ok := strings.Cut(v, ":")
	if !ok {
		return 0, fmt.Errorf("%q non è un orario", v)
	}
	ore, err := strconv.Atoi(o)
	if err != nil || len(o) != 2 || ore < 0 || ore > 23 {
		return 0, fmt.Errorf("%q non è un orario", v)
	}
	min, err := strconv.Atoi(m)
	if err != nil || len(m) != 2 || min < 0 || min > 59 {
		return 0, fmt.Errorf("%q non è un orario", v)
	}
	return ore*60 + min, nil
}

// dentro dice se adesso cade in almeno una delle fasce.
//
// Nessuna fascia vuol dire sempre, e non mai: è il comportamento di chi non ha
// configurato niente — cioè di tutti quelli che erano abbonati prima che le
// fasce esistessero — e cambiarglielo sotto vorrebbe dire spegnere le
// notifiche a qualcuno senza che l'abbia chiesto.
func dentro(fasce []Fascia, adesso time.Time) bool {
	if len(fasce) == 0 {
		return true
	}
	for _, f := range fasce {
		if f.contiene(adesso) {
			return true
		}
	}
	return false
}
