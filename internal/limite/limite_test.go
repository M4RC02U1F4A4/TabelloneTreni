package limite

import (
	"testing"
	"time"
)

func TestScortaPoiRitmo(t *testing.T) {
	adesso := time.Unix(0, 0)
	s := Nuovo(3, time.Second)
	s.ora = func() time.Time { return adesso }

	for i := range 3 {
		if !s.Consenti("a") {
			t.Fatalf("richiesta %d rifiutata dentro la scorta", i)
		}
	}
	if s.Consenti("a") {
		t.Fatal("scorta finita, eppure passa")
	}
	if !s.Consenti("b") {
		t.Fatal("un altro indirizzo ha la sua scorta")
	}
	adesso = adesso.Add(time.Second)
	if !s.Consenti("a") || s.Consenti("a") {
		t.Fatal("dopo un secondo ne passa una e una sola")
	}
	adesso = adesso.Add(time.Hour)
	for range 3 {
		if !s.Consenti("a") {
			t.Fatal("dopo un'ora la scorta è di nuovo piena")
		}
	}
	if s.Consenti("a") {
		t.Fatal("la scorta non cresce oltre il tetto")
	}
}

func TestSfoltisceSoloISecchiPieni(t *testing.T) {
	adesso := time.Unix(0, 0)
	s := Nuovo(2, time.Second)
	s.ora = func() time.Time { return adesso }
	s.Consenti("vecchio")
	adesso = adesso.Add(time.Minute)
	s.Consenti("recente")
	s.sfoltisci(adesso)
	if _, c := s.secchi["vecchio"]; c {
		t.Error("il secchio ormai pieno doveva sparire")
	}
	if _, c := s.secchi["recente"]; !c {
		t.Error("il secchio in uso doveva restare")
	}
}
