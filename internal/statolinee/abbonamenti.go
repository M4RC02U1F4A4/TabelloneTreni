package statolinee

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// Quanto si accetta da chi si abbona. Non sono numeri di capacità: l'endpoint
// è raggiungibile da chiunque apra l'applicazione, e senza un tetto una sola
// richiesta ripetuta riempirebbe il disco.
const (
	MaxAbbonamenti         = 500
	MaxLineePerAbbonamento = 100
)

// Abbonamento è un telefono che vuole essere avvisato su certe linee.
type Abbonamento struct {
	Sottoscrizione webpush.Subscription `json:"subscription"`
	Linee          []string             `json:"lines"`
}

// Abbonati custodisce gli abbonamenti e li tiene su disco.
//
// Il formato è un file JSON riscritto per intero a ogni modifica: gli
// abbonamenti sono decine, si toccano quando qualcuno accende una campanella,
// e un database qui costerebbe più di quanto risolve. Quello che invece serve
// davvero è che la scrittura sia atomica — un file troncato a metà da un
// riavvio perderebbe tutti gli abbonati insieme.
type Abbonati struct {
	mu       sync.RWMutex
	percorso string // vuoto: si tiene tutto in memoria e basta
	m        map[string]Abbonamento
}

func ApriAbbonati(percorso string) (*Abbonati, error) {
	a := &Abbonati{percorso: percorso, m: map[string]Abbonamento{}}
	if percorso == "" {
		return a, nil
	}
	b, err := os.ReadFile(percorso)
	if errors.Is(err, os.ErrNotExist) {
		return a, nil // primo avvio
	}
	if err != nil {
		return nil, err
	}
	var elenco []Abbonamento
	if err := json.Unmarshal(b, &elenco); err != nil {
		return nil, fmt.Errorf("%s: %w", percorso, err)
	}
	for _, ab := range elenco {
		a.m[ab.Sottoscrizione.Endpoint] = ab
	}
	return a, nil
}

func (a *Abbonati) Quanti() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.m)
}

// Registra aggiunge o aggiorna un abbonamento. Un elenco di linee vuoto lo
// cancella: è lo stesso gesto visto dall'altra parte — chi spegne l'ultima
// campanella non vuole più essere avvisato, e tenerne memoria non serve a
// nessuno.
func (a *Abbonati) Registra(ab Abbonamento) error {
	if err := valida(ab); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(ab.Linee) == 0 {
		delete(a.m, ab.Sottoscrizione.Endpoint)
	} else {
		if _, gia := a.m[ab.Sottoscrizione.Endpoint]; !gia && len(a.m) >= MaxAbbonamenti {
			return errors.New("troppi abbonamenti")
		}
		a.m[ab.Sottoscrizione.Endpoint] = ab
	}
	return a.scrivi()
}

// Dimentica toglie un abbonamento. Lo si chiama quando il servizio push
// risponde che quell'indirizzo non esiste più: il telefono ha disinstallato
// l'app o revocato il permesso, e insistere è solo traffico.
func (a *Abbonati) Dimentica(endpoint string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, c := a.m[endpoint]; !c {
		return
	}
	delete(a.m, endpoint)
	a.scrivi()
}

// PerLinea restituisce chi segue quella linea.
func (a *Abbonati) PerLinea(codice string) []Abbonamento {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var out []Abbonamento
	for _, ab := range a.m {
		if slices.Contains(ab.Linee, codice) {
			out = append(out, ab)
		}
	}
	return out
}

// scrivi va chiamata con il lock preso.
func (a *Abbonati) scrivi() error {
	if a.percorso == "" {
		return nil
	}
	elenco := make([]Abbonamento, 0, len(a.m))
	for _, ab := range a.m {
		elenco = append(elenco, ab)
	}
	b, err := json.Marshal(elenco)
	if err != nil {
		return err
	}
	// Scrittura atomica: si scrive accanto e si rinomina. Il rename è atomico
	// sullo stesso filesystem, quindi il file buono o è quello vecchio o è
	// quello nuovo, mai mezzo dell'uno e mezzo dell'altro.
	tmp, err := os.CreateTemp(filepath.Dir(a.percorso), ".abbonamenti-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), a.percorso)
}

func valida(ab Abbonamento) error {
	u, err := url.Parse(ab.Sottoscrizione.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("endpoint non valido")
	}
	if len(ab.Linee) > MaxLineePerAbbonamento {
		return errors.New("troppe linee")
	}
	// Senza chiavi non si puo' cifrare niente: l'abbonamento sarebbe accettato
	// e poi fallirebbe a ogni invio, in silenzio.
	if len(ab.Linee) > 0 && (ab.Sottoscrizione.Keys.Auth == "" || ab.Sottoscrizione.Keys.P256dh == "") {
		return errors.New("chiavi mancanti")
	}
	return nil
}
