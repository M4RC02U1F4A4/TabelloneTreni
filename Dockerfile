# La compilazione avviene sempre sull'architettura del runner e produce il
# binario per quella di destinazione: Go fa cross-compiling nativamente, e così
# l'immagine multi-architettura si costruisce senza emulazione QEMU, che per un
# build arm64 su runner amd64 costerebbe minuti invece di secondi.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

WORKDIR /src
# I moduli si scaricano prima del resto del sorgente, così la cache di questo
# livello sopravvive a ogni modifica al codice.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG TARGETOS TARGETARCH
ARG VERSIONE=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.versione=$VERSIONE" -o /tabellonetreni .

# distroless static: nessuna shell e nessun gestore di pacchetti, ma con i
# certificati radice, che servono per parlare in TLS con iechub.rfi.it.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /tabellonetreni /tabellonetreni

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/tabellonetreni"]
