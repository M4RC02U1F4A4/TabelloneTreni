// Comando statolinee segue lo stato di circolazione delle linee Trenord e lo
// espone al tabellone, che glielo chiede via HTTP.
//
// È un servizio a sé perché interroga Trenord a ritmo suo e una volta sola per
// tutti, mentre il tabellone risponde a ogni telefono: se stesse dentro,
// scalare l'uno moltiplicherebbe le richieste dell'altro.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/statolinee"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/trenord"
)

var versione = "dev"

func main() {
	log.SetFlags(log.Ltime)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	abbonati, err := statolinee.ApriAbbonati(percorsoDati("abbonamenti.json"))
	if err != nil {
		log.Fatal(err)
	}
	// Le chiavi se le fa il servizio al primo avvio e se le tiene accanto agli
	// abbonamenti: non c'e' niente da creare a mano, e le due cose vivono e
	// muoiono insieme, che e' l'unico modo in cui ha senso.
	pubblica, privata, err := statolinee.ApriChiavi(percorsoDati("chiavi.json"))
	if err != nil {
		log.Fatal(err)
	}
	notificatore := statolinee.NuovoNotificatore(abbonati, pubblica, privata, "")
	log.Printf("notifiche accese (%d abbonamenti)", abbonati.Quanti())
	if os.Getenv("DATI") == "" {
		log.Print("DATI non impostata: chiavi e abbonamenti si perdono al riavvio")
	}

	svc := statolinee.Nuovo(trenord.NewClient()).ConNotifiche(abbonati, notificatore)
	go svc.Osserva(ctx)

	srv := &http.Server{
		Addr:              indirizzo(),
		Handler:           svc.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		log.Printf("statolinee %s in ascolto su %s (letture ogni %s)",
			versione, srv.Addr, statolinee.Intervallo)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	log.Print("arresto in corso")
	chiusura, annulla := context.WithTimeout(context.Background(), 5*time.Second)
	defer annulla()
	if err := srv.Shutdown(chiusura); err != nil {
		log.Printf("arresto forzato: %v", err)
	}
}

// percorsoDati dice dove tenere le cose che devono sopravvivere al riavvio:
// gli abbonamenti alle notifiche e le chiavi con cui si spediscono. Vuoto
// significa solo in memoria, il che va bene per provare in locale e non va bene
// in produzione, dove i riavvii sono uno per rilascio.
func percorsoDati(nome string) string {
	d := os.Getenv("DATI")
	if d == "" {
		return ""
	}
	return filepath.Join(d, nome)
}

func indirizzo() string {
	if a := os.Getenv("ADDR"); a != "" {
		return a
	}
	if p := os.Getenv("PORT"); p != "" {
		return ":" + p
	}
	return ":8081"
}
