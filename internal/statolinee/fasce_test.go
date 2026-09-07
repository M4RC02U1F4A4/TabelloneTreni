package statolinee

import (
	"testing"
	"time"
)

// Il 7 settembre 2026 è un lunedì: le date qui sotto contano su questo.
func lunedi(ora, min int) time.Time {
	return time.Date(2026, 9, 7, ora, min, 0, 0, time.UTC)
}

func giorno(g time.Weekday, ora, min int) time.Time {
	// 6 settembre 2026 è una domenica, cioè il giorno 0 della settimana.
	return time.Date(2026, 9, 6+int(g), ora, min, 0, 0, time.UTC)
}

func TestLaFasciaDiChiVaAlLavoro(t *testing.T) {
	// Andata e ritorno dal lunedì al venerdì.
	fasce := []Fascia{
		{Giorni: []int{1, 2, 3, 4, 5}, Da: "07:00", A: "09:00"},
		{Giorni: []int{1, 2, 3, 4, 5}, Da: "17:00", A: "19:00"},
	}
	casi := []struct {
		nome   string
		quando time.Time
		atteso bool
	}{
		{"prima dell'andata", lunedi(6, 59), false},
		{"il minuto in cui apre", lunedi(7, 0), true},
		{"in mezzo all'andata", lunedi(8, 30), true},
		// Le 9 in punto sono fuori: è come si leggono gli orari di apertura.
		{"il minuto in cui chiude", lunedi(9, 0), false},
		{"a metà giornata", lunedi(13, 0), false},
		{"in mezzo al ritorno", lunedi(18, 15), true},
		{"la sera", lunedi(21, 0), false},
		{"la notte", lunedi(3, 0), false},
		// Il sabato non si va al lavoro: la fascia non c'è.
		{"sabato mattina", giorno(time.Saturday, 8, 0), false},
		{"domenica sera", giorno(time.Sunday, 18, 0), false},
		{"venerdì sera", giorno(time.Friday, 18, 0), true},
	}
	for _, c := range casi {
		t.Run(c.nome, func(t *testing.T) {
			if got := dentro(fasce, c.quando); got != c.atteso {
				t.Errorf("dentro(%s) = %v, atteso %v",
					c.quando.Format("Mon 15:04"), got, c.atteso)
			}
		})
	}
}

// Nessuna fascia è il caso di chi non ha configurato niente, compresi tutti
// quelli che erano abbonati prima che le fasce esistessero: per loro non
// cambia nulla.
func TestNessunaFasciaVuolDireSempre(t *testing.T) {
	for _, q := range []time.Time{lunedi(3, 0), lunedi(8, 0), giorno(time.Sunday, 23, 59)} {
		if !dentro(nil, q) {
			t.Errorf("%s: senza fasce non è in ascolto", q.Format("Mon 15:04"))
		}
	}
}

// Giorni vuoti dentro una fascia sono "tutti i giorni": è come si dice sempre
// senza elencare sette numeri.
func TestUnaFasciaSenzaGiorniValeOgniGiorno(t *testing.T) {
	f := []Fascia{{Da: "07:00", A: "09:00"}}
	for g := time.Sunday; g <= time.Saturday; g++ {
		if !dentro(f, giorno(g, 8, 0)) {
			t.Errorf("%s alle 8 non è in ascolto", g)
		}
		if dentro(f, giorno(g, 10, 0)) {
			t.Errorf("%s alle 10 è in ascolto", g)
		}
	}
}

// Una fascia che chiude prima di aprire scavalca la mezzanotte, e i giorni
// sono quelli in cui apre. È il turno di notte.
func TestUnaFasciaPuoScavalcareLaMezzanotte(t *testing.T) {
	f := []Fascia{{Giorni: []int{int(time.Saturday)}, Da: "22:00", A: "02:00"}}
	casi := []struct {
		nome   string
		quando time.Time
		atteso bool
	}{
		{"sabato prima", giorno(time.Saturday, 21, 59), false},
		{"sabato sera", giorno(time.Saturday, 23, 0), true},
		{"domenica notte, ancora dentro", giorno(time.Sunday, 1, 30), true},
		{"domenica notte, chiusa", giorno(time.Sunday, 2, 0), false},
		// Non è la fascia della domenica sera: quella comincia il sabato.
		{"domenica sera", giorno(time.Sunday, 23, 0), false},
	}
	for _, c := range casi {
		t.Run(c.nome, func(t *testing.T) {
			if got := dentro(f, c.quando); got != c.atteso {
				t.Errorf("dentro(%s) = %v, atteso %v",
					c.quando.Format("Mon 15:04"), got, c.atteso)
			}
		})
	}
}

// La domenica notte sfocia nel lunedì, che è l'altro capo della settimana:
// il conto va fatto modulo la settimana e non modulo il giorno.
func TestLaFasciaDellaDomenicaSfociaNelLunedi(t *testing.T) {
	f := []Fascia{{Giorni: []int{int(time.Sunday)}, Da: "23:00", A: "01:00"}}
	if !dentro(f, giorno(time.Monday, 0, 30)) {
		t.Error("lunedì all'una meno mezza non è in ascolto")
	}
	if dentro(f, giorno(time.Monday, 1, 30)) {
		t.Error("lunedì all'una e mezza è in ascolto")
	}
}

func TestFasceRifiutate(t *testing.T) {
	casi := map[string]Fascia{
		"senza i due punti":       {Da: "0700", A: "09:00"},
		"ora impossibile":         {Da: "25:00", A: "09:00"},
		"minuto impossibile":      {Da: "07:70", A: "09:00"},
		"una cifra sola":          {Da: "7:00", A: "09:00"},
		"vuota":                   {Da: "", A: "09:00"},
		"estremi uguali":          {Da: "07:00", A: "07:00"},
		"giorno fuori settimana":  {Giorni: []int{7}, Da: "07:00", A: "09:00"},
		"giorno negativo":         {Giorni: []int{-1}, Da: "07:00", A: "09:00"},
		"testo al posto dell'ora": {Da: "ab:cd", A: "09:00"},
	}
	for nome, f := range casi {
		t.Run(nome, func(t *testing.T) {
			if err := f.valida(); err == nil {
				t.Error("accettata")
			}
			// Una fascia che non si legge non tiene in ascolto nessuno: se una
			// arrivasse comunque nello schedario, il silenzio è il verso
			// giusto in cui sbagliare.
			if f.contiene(lunedi(8, 0)) {
				t.Error("una fascia non valida tiene in ascolto")
			}
		})
	}
}

func TestFasceAccettate(t *testing.T) {
	for _, f := range []Fascia{
		{Da: "00:00", A: "23:59"},
		{Da: "07:00", A: "09:00"},
		{Da: "22:00", A: "02:00"},
		{Giorni: []int{0, 6}, Da: "09:30", A: "10:45"},
	} {
		if err := f.valida(); err != nil {
			t.Errorf("%+v rifiutata: %v", f, err)
		}
	}
}
