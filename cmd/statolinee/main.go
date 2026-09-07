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
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/statolinee"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/trenord"
)

var versione = "dev"

func main() {
	log.SetFlags(log.Ltime)

	// Le chiavi VAPID si generano una volta sola e poi restano: se cambiano,
	// tutti gli abbonamenti gia' presi diventano inservibili. Sta qui invece
	// che in un comando a parte perche' e' una riga di lavoro, e cercarla dove
	// gira il servizio e' piu' facile che ricordarsi che esiste un altro
	// binario.
	generaChiavi := flag.Bool("chiavi", false, "genera una coppia di chiavi VAPID ed esci")
	flag.Parse()
	if *generaChiavi {
		privata, pubblica, err := webpush.GenerateVAPIDKeys()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("VAPID_PUBLIC=%s\nVAPID_PRIVATE=%s\n", pubblica, privata)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	abbonati, err := statolinee.ApriAbbonati(percorsoAbbonamenti())
	if err != nil {
		log.Fatal(err)
	}
	notificatore := statolinee.NuovoNotificatore(abbonati,
		os.Getenv("VAPID_PUBLIC"), os.Getenv("VAPID_PRIVATE"), os.Getenv("VAPID_SUBJECT"))
	if notificatore == nil {
		log.Print("notifiche spente: VAPID_PUBLIC e VAPID_PRIVATE non impostate")
	} else {
		log.Printf("notifiche accese (%d abbonamenti)", abbonati.Quanti())
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

// percorsoAbbonamenti dice dove tenere gli abbonamenti alle notifiche. Vuoto
// significa solo in memoria: si perdono a ogni riavvio, il che va bene per
// provare in locale e non va bene in produzione.
func percorsoAbbonamenti() string {
	d := os.Getenv("DATI")
	if d == "" {
		return ""
	}
	return filepath.Join(d, "abbonamenti.json")
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
