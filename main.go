// Comando tabellonetreni serve una versione leggera e per telefono dei
// tabelloni di RFI, con in più il filtro per stazione di arrivo.
//
// Il server esiste per due motivi che il browser da solo non può risolvere:
// RFI non manda intestazioni CORS, e la sua pagina pesa quasi 300 KB non
// compressi, che diventano una decina di KB una volta ridotti a dati.
package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/api"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/board"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/rfi"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/stations"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/vt"
)

// Il database dei fusi orari va dentro il binario: gli orari dei treni sono in
// Europe/Rome e vanno formattati lì, mentre l'immagine finale è una distroless
// static, dove non c'è nessun /usr/share/zoneinfo su cui contare.
import _ "time/tzdata"

//go:embed all:web
var contenutoWeb embed.FS

// versione la sostituisce il build con -ldflags; a mano resta "dev".
var versione = "dev"

func main() {
	log.SetFlags(log.Ltime)

	// Go non conosce .webmanifest e lo servirebbe come text/plain, con cui
	// il browser non installa l'applicazione.
	if err := mime.AddExtensionType(".webmanifest", "application/manifest+json"); err != nil {
		log.Fatal(err)
	}

	statici, err := fs.Sub(contenutoWeb, "web")
	if err != nil {
		log.Fatal(err)
	}
	svc := board.New(rfi.NewClient(), stations.Default).ConLive(vt.NewClient())
	srv := &http.Server{
		Addr:              indirizzo(),
		Handler:           api.New(svc, stations.Default, statici).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("tabellonetreni %s in ascolto su %s (%d stazioni, catalogo del %s)",
			versione, srv.Addr, len(stations.Default.Elenco), stations.Default.Generated)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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
	return ":8080"
}
