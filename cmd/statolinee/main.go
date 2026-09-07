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

	svc := statolinee.Nuovo(trenord.NewClient())
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

func indirizzo() string {
	if a := os.Getenv("ADDR"); a != "" {
		return a
	}
	if p := os.Getenv("PORT"); p != "" {
		return ":" + p
	}
	return ":8081"
}
