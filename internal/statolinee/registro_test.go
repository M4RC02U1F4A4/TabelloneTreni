package statolinee

import (
	"testing"
	"time"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/trenord"
)

func linee(coppie ...any) []trenord.Linea {
	var out []trenord.Linea
	for i := 0; i < len(coppie); i += 2 {
		out = append(out, trenord.Linea{
			Codice: coppie[i].(string),
			Nome:   coppie[i].(string) + " prova",
			Stato:  coppie[i+1].(trenord.Stato),
		})
	}
	return out
}

var quando = time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)

// Il primo giro non è mai una notizia: senza questa regola ogni riavvio del
// servizio manderebbe una notifica per ogni linea che in quel momento non è
// regolare — e i riavvii sono uno per rilascio.
func TestPrimoGiroNonProduceCambi(t *testing.T) {
	r := NuovoRegistro()
	cambi := r.Aggiorna(linee("S1", trenord.Regolare, "S2", trenord.Critico), quando)
	if len(cambi) != 0 {
		t.Fatalf("cambi = %+v, atteso nessuno", cambi)
	}
	got, agg := r.Linee()
	if len(got) != 2 || !agg.Equal(quando) {
		t.Fatalf("stato = %+v @ %v", got, agg)
	}
}

func TestCambiDiStato(t *testing.T) {
	r := NuovoRegistro()
	r.Aggiorna(linee("S1", trenord.Regolare, "S2", trenord.Critico, "S3", trenord.Regolare), quando)

	cambi := r.Aggiorna(linee(
		"S1", trenord.Critico, // peggiora
		"S2", trenord.Regolare, // torna a posto
		"S3", trenord.Regolare, // invariata
	), quando.Add(5*time.Minute))

	if len(cambi) != 2 {
		t.Fatalf("cambi = %+v, attesi 2", cambi)
	}
	// Lo stato di partenza deve arrivare con il cambio: "torna regolare" e
	// "diventa critica" sono due notifiche diverse.
	for _, c := range cambi {
		switch c.Linea.Codice {
		case "S1":
			if c.Prima != trenord.Regolare || c.Linea.Stato != trenord.Critico {
				t.Errorf("S1: %s -> %s", c.Prima, c.Linea.Stato)
			}
		case "S2":
			if c.Prima != trenord.Critico || c.Linea.Stato != trenord.Regolare {
				t.Errorf("S2: %s -> %s", c.Prima, c.Linea.Stato)
			}
		default:
			t.Errorf("cambio inatteso su %s", c.Linea.Codice)
		}
	}
}

// Trenord aggiunge e toglie linee. Una che compare adesso non ha un "prima",
// e annunciarla come peggiorata sarebbe inventarsi una storia; una che sparisce
// non ha nessuno a cui interessare.
func TestLineeCheVannoEVengono(t *testing.T) {
	r := NuovoRegistro()
	r.Aggiorna(linee("S1", trenord.Regolare, "S2", trenord.Critico), quando)

	cambi := r.Aggiorna(linee("S1", trenord.Regolare, "S99", trenord.Grave), quando.Add(time.Minute))
	if len(cambi) != 0 {
		t.Fatalf("cambi = %+v, atteso nessuno", cambi)
	}
	// E la linea sparita non deve restare nell'elenco servito.
	got, _ := r.Linee()
	if len(got) != 2 || got[1].Codice != "S99" {
		t.Fatalf("stato = %+v", got)
	}
}

// Una linea che oscilla fra due giri deve produrre un cambio per giro, non uno
// solo: chi ha la campanellina accesa vuole sapere anche che è tornata a posto.
func TestOscillazione(t *testing.T) {
	r := NuovoRegistro()
	r.Aggiorna(linee("S1", trenord.Regolare), quando)
	for i, atteso := range []trenord.Stato{trenord.Critico, trenord.Regolare, trenord.Grave} {
		cambi := r.Aggiorna(linee("S1", atteso), quando.Add(time.Duration(i+1)*time.Minute))
		if len(cambi) != 1 || cambi[0].Linea.Stato != atteso {
			t.Fatalf("giro %d: cambi = %+v", i, cambi)
		}
	}
}
