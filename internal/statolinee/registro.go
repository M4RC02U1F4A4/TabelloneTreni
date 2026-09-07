// Package statolinee tiene lo stato di circolazione delle linee Trenord e
// riconosce quando cambia.
//
// Sta in un servizio a parte dal tabellone per tre motivi: interroga Trenord a
// intervalli suoi e deve farlo una volta sola per tutti, prima o poi dovrà
// ricordarsi gli abbonamenti alle notifiche — cioè avere stato su disco, che
// il tabellone non ha e non vuole — e se cade non deve portarsi dietro i
// tabelloni, che sono la ragione per cui l'applicazione esiste.
package statolinee

import (
	"sync"
	"time"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/trenord"
)

// Cambio è un bollino che ha cambiato colore. Porta con sé lo stato di prima:
// "torna regolare" e "diventa critica" sono due notizie diverse, e chi manda
// la notifica deve poterle distinguere senza tenere una memoria propria.
type Cambio struct {
	Linea trenord.Linea
	Prima trenord.Stato
}

// Registro custodisce l'ultimo stato buono e lo confronta con quello nuovo.
type Registro struct {
	mu         sync.RWMutex
	linee      []trenord.Linea
	precedente map[string]trenord.Stato
	aggiornato time.Time
	avvisi     map[string][]trenord.Avviso
}

func NuovoRegistro() *Registro {
	return &Registro{avvisi: map[string][]trenord.Avviso{}}
}

// Aggiorna sostituisce lo stato e restituisce i bollini cambiati.
//
// Il primo aggiornamento non produce mai cambi: al primo giro tutto è "nuovo",
// e senza questa regola ogni riavvio del servizio manderebbe una notifica per
// ogni linea che in quel momento non è regolare.
func (r *Registro) Aggiorna(nuove []trenord.Linea, adesso time.Time) []Cambio {
	r.mu.Lock()
	defer r.mu.Unlock()

	var cambi []Cambio
	corrente := make(map[string]trenord.Stato, len(nuove))
	for _, l := range nuove {
		corrente[l.Codice] = l.Stato
		if r.precedente == nil {
			continue
		}
		// Una linea che non c'era prima non è un cambio: non si sa da dove
		// venga, e annunciarla come peggiorata sarebbe inventarsi una storia.
		if prima, esisteva := r.precedente[l.Codice]; esisteva && prima != l.Stato {
			cambi = append(cambi, Cambio{Linea: l, Prima: prima})
		}
	}

	r.linee = nuove
	r.precedente = corrente
	r.aggiornato = adesso
	return cambi
}

// Linee restituisce l'ultimo stato buono e quando è stato letto. Il tempo zero
// significa che non si è ancora riusciti a leggere niente.
func (r *Registro) Linee() ([]trenord.Linea, time.Time) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.linee, r.aggiornato
}

// MettiAvvisi sostituisce le comunicazioni di una linea e restituisce quelle
// che prima non c'erano.
//
// Il confronto è sul testo e non sulla data: Trenord ripubblica lo stesso
// avviso con l'ora aggiornata quando lo ritocca, e avvisare due volte della
// stessa cosa è il modo più rapido per far spegnere le notifiche.
func (r *Registro) MettiAvvisi(codice string, nuovi []trenord.Avviso) []trenord.Avviso {
	r.mu.Lock()
	defer r.mu.Unlock()

	vecchi, cera := r.avvisi[codice]
	r.avvisi[codice] = nuovi
	// Prima lettura: niente è "nuovo", come per i bollini. Altrimenti ogni
	// riavvio riannuncerebbe i lavori annunciati a agosto.
	if !cera {
		return nil
	}
	conosciuti := make(map[string]bool, len(vecchi))
	for _, a := range vecchi {
		conosciuti[a.Testo] = true
	}
	var freschi []trenord.Avviso
	for _, a := range nuovi {
		if !conosciuti[a.Testo] {
			freschi = append(freschi, a)
		}
	}
	return freschi
}

// AvvisiDi restituisce le comunicazioni note per una linea.
func (r *Registro) AvvisiDi(codice string) []trenord.Avviso {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.avvisi[codice]
}
