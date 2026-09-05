// Package rfi legge i tabelloni di iechub.rfi.it e li riduce a dati.
//
// La pagina del monitor è HTML renderizzato dal server: non esiste alcun
// endpoint JSON, e il suo JavaScript non fa nemmeno una chiamata di rete. Il
// solo modo di avere i dati è quindi interpretarne il markup — e il markup può
// cambiare senza preavviso, per cui il parsing sta tutto qui dietro e i test su
// pagine reali salvate in testdata sono la rete di sicurezza.
package rfi

// Stop è una fermata successiva del treno, con l'orario previsto in quella
// stazione. Il nome arriva abbreviato ("MI BOVISA P.") e non coincide con
// quello del catalogo: va risolto con stations.Matcher.
type Stop struct {
	Name string `json:"name"`
	Time string `json:"time"`
}

// Train è una riga del tabellone.
type Train struct {
	Number   string `json:"number"`
	Carrier  string `json:"carrier,omitempty"`
	Category string `json:"category,omitempty"`
	// Terminus è la destinazione sulle partenze, la provenienza sugli arrivi.
	Terminus  string `json:"terminus"`
	Time      string `json:"time"`
	Delay     int    `json:"delay,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty"`
	// Status è il contenuto della cella ritardo quando non è né un numero né
	// una cancellazione: RFI ci scrive anche testi come "RITARDO", per un
	// ritardo annunciato ma non ancora quantificato. Va mostrato così com'è,
	// perché l'insieme dei valori possibili non è documentato da nessuna parte.
	Status   string `json:"status,omitempty"`
	Platform string `json:"platform,omitempty"`
	// Boarding è il lampeggio del tabellone: treno in partenza o in arrivo.
	Boarding bool   `json:"boarding,omitempty"`
	Notes    string `json:"notes,omitempty"`
	// Arrival è l'orario in cui il treno arriva alla stazione scelta come
	// destinazione. Lo riempie il filtro, quindi è vuoto sul tabellone crudo.
	Arrival string `json:"arrival,omitempty"`
	// LiveDelay è il ritardo che ViaggiaTreno misura sul treno vero, in minuti.
	// Come Arrival, non viene da questo pacchetto: lo attacca il servizio
	// tabellone dopo aver letto la seconda fonte.
	//
	// È un puntatore perché qui lo zero è un'informazione — "misurato, in
	// orario" — e va distinto dall'assenza di misura, che capita quando
	// ViaggiaTreno non ha quel treno o non l'ha ancora rilevato da nessuna
	// parte. Delay, sopra, non ha lo stesso problema: RFI lo stampa comunque.
	LiveDelay *int `json:"liveDelay,omitempty"`
	// PlatformChanged dice che il treno parte da un binario diverso da quello
	// previsto. Come LiveDelay, lo attacca il servizio tabellone leggendo la
	// seconda fonte: RFI pubblica una casella sola, dalla quale non si vede se
	// il numero che c'è dentro è quello di sempre o quello di stasera.
	PlatformChanged bool `json:"platformChanged,omitempty"`
	// I due binari secondo la seconda fonte, valorizzati solo quando
	// differiscono: servono a dire *da quale* binario il treno si è spostato,
	// che a chi si è già incamminato interessa quanto sapere che si è spostato.
	PlatformScheduled string `json:"platformScheduled,omitempty"`
	PlatformActual    string `json:"platformActual,omitempty"`
	// Stops è vuoto sui tabelloni degli arrivi: RFI pubblica le fermate
	// successive solo per le partenze.
	Stops []Stop `json:"stops,omitempty"`
}

// Board è un tabellone completo.
type Board struct {
	PlaceID  int     `json:"placeId"`
	Station  string  `json:"station"`
	Arrivals bool    `json:"arrivals"`
	Updated  string  `json:"updated,omitempty"`
	Trains   []Train `json:"trains"`
}
