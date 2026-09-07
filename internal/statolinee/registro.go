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

	// Quello che si sa dal dettaglio di una linea, che è l'unica fonte con un
	// orario e quindi l'unica su cui si possano mandare notifiche.
	dettagli map[string]*dettaglioNoto
}

type dettaglioNoto struct {
	stato      trenord.Stato
	aggiornato time.Time
}

func NuovoRegistro() *Registro {
	return &Registro{
		avvisi:   map[string][]trenord.Avviso{},
		dettagli: map[string]*dettaglioNoto{},
	}
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

// Novita è quello che è cambiato su una linea da quando la si è guardata
// l'ultima volta, e su cui vale la pena avvisare qualcuno.
type Novita struct {
	Cambio *Cambio          // nil se il bollino non si è mosso
	Avvisi []trenord.Avviso // le comunicazioni che prima non c'erano

	codice string
	nome   string
}

// MettiDettaglio registra quello che dice la pagina di una linea e restituisce
// le novità.
//
// Scarta le risposte vecchie. Serve perché lo stesso indirizzo, interrogato due
// volte di fila, risponde da backend che non concordano: misurato, dieci
// richieste consecutive danno due varianti a caso, e differiscono su una
// dozzina di linee. Prendendole per buone tutte, un quarto delle linee sembra
// cambiare stato ogni pochi minuti, e chi ha la campanella accesa riceve
// notifiche per movimenti che non esistono. L'orario di aggiornamento dice
// quale delle due risposte è quella di adesso, e indietro non si torna.
func (r *Registro) MettiDettaglio(codice string, nome string, d *trenord.Dettaglio) Novita {
	r.mu.Lock()
	defer r.mu.Unlock()

	noto := r.dettagli[codice]
	if noto != nil && !d.Aggiornato.IsZero() && d.Aggiornato.Before(noto.aggiornato) {
		// Risposta dal backend rimasto indietro: non dice niente di nuovo, e
		// quello che dice è vecchio.
		return Novita{}
	}

	n := Novita{codice: codice, nome: nome}
	if noto != nil && noto.stato != d.Stato {
		n.Cambio = &Cambio{
			Linea: trenord.Linea{Codice: codice, Nome: nome, Stato: d.Stato},
			Prima: noto.stato,
		}
	}
	r.dettagli[codice] = &dettaglioNoto{stato: d.Stato, aggiornato: d.Aggiornato}
	n.Avvisi = r.mettiAvvisi(codice, d.Avvisi)
	return n
}

// StatoNoto dice cosa il dettaglio ha detto per ultimo su una linea. Il
// secondo valore è falso se la linea non è mai stata guardata da vicino.
func (r *Registro) StatoNoto(codice string) (trenord.Stato, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if d := r.dettagli[codice]; d != nil {
		return d.stato, true
	}
	return trenord.Regolare, false
}

// mettiAvvisi va chiamata con il lock preso.
//
// Il confronto è sul testo e non sulla data: Trenord ripubblica lo stesso
// avviso con l'ora aggiornata quando lo ritocca, e avvisare due volte della
// stessa cosa è il modo più rapido per far spegnere le notifiche.
func (r *Registro) mettiAvvisi(codice string, nuovi []trenord.Avviso) []trenord.Avviso {
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

// NomeDi è il nome per esteso di una linea, che è quello che finisce nel titolo
// della notifica: il codice da solo non dice niente a nessuno. Se l'elenco non
// è ancora arrivato resta il codice, che è meglio di una notifica senza titolo.
func (r *Registro) NomeDi(codice string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, l := range r.linee {
		if l.Codice == codice {
			return l.Nome
		}
	}
	return codice
}

// AvvisiDi restituisce le comunicazioni note per una linea.
func (r *Registro) AvvisiDi(codice string) []trenord.Avviso {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.avvisi[codice]
}
