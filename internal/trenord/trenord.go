// Package trenord legge lo stato di circolazione delle linee Trenord.
//
// La fonte è la pagina "Le nostre linee", che è servita già composta dal
// server: lo stato di ogni linea sta nell'HTML, non in una chiamata a parte,
// quindi basta una richiesta e nessun browser.
package trenord

// Stato è il semaforo che Trenord mostra accanto a ogni linea.
//
// I tre valori, e il fatto che siano tre, vengono dallo script che la pagina
// stessa esegue per ricostruire la mappa codice->stato: cerca le classi
// green-line, critical e danger, in quest'ordine di gravità.
type Stato int

const (
	Regolare Stato = iota
	Critico
	Grave
)

func (s Stato) String() string {
	switch s {
	case Regolare:
		return "regolare"
	case Critico:
		return "critico"
	case Grave:
		return "grave"
	}
	return "sconosciuto"
}

// Linea è una linea con il suo stato attuale.
type Linea struct {
	// Codice è l'identificatore di Trenord: "S1", "R16", "RE_13". È l'unica
	// cosa stabile su cui appoggiare una preferenza salvata sul telefono — il
	// nome cambia quando cambia il capolinea.
	Codice string `json:"code"`
	Nome   string `json:"name"`
	// Gruppo è l'intestazione sotto cui Trenord raccoglie la linea
	// ("REGIONALI", "LINEE SUBURBANE"): serve a ritrovare la propria linea in
	// un elenco di una sessantina di voci.
	Gruppo string `json:"group"`
	Stato  Stato  `json:"status"`
}
