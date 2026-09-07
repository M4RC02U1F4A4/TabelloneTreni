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
# I due binari stanno nella stessa immagine invece che in due: pesano pochi MB
# l'uno, si rilasciano insieme perché vengono dallo stesso commit, e così la
# pipeline resta una sola. A separarli sono i due servizi in compose, che la
# avviano con entrypoint diversi.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.versione=$VERSIONE" -o /tabellonetreni . && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.versione=$VERSIONE" -o /statolinee ./cmd/statolinee

# distroless static: nessuna shell e nessun gestore di pacchetti, ma con i
# certificati radice, che servono per parlare in TLS con iechub.rfi.it e
# www.trenord.it.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /tabellonetreni /tabellonetreni
COPY --from=build /statolinee /statolinee

EXPOSE 8080 8081
USER nonroot:nonroot
ENTRYPOINT ["/tabellonetreni"]
