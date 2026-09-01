package api

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/board"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/rfi"
	"github.com/M4RC02U1F4A4/TabelloneTreni/internal/stations"
)

type sorgenteFinta struct{}

func (sorgenteFinta) Fetch(ctx context.Context, placeID int, arrivals bool) (*rfi.Board, error) {
	return &rfi.Board{
		PlaceID: placeID, Station: "PROVA", Arrivals: arrivals,
		Trains: []rfi.Train{{Number: "1", Time: "10:00", Terminus: "ALTROVE"}},
	}, nil
}

func server() http.Handler {
	statici := fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>x</title>" + string(make([]byte, 2000)))},
		"icona-180.png": {Data: []byte("\x89PNG\r\n\x1a\n" + string(make([]byte, 2000)))},
	}
	return New(board.New(sorgenteFinta{}, stations.Default), stations.Default, statici).Handler()
}

func chiedi(t *testing.T, h http.Handler, percorso string, intestazioni map[string]string) *http.Response {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, percorso, nil)
	for k, v := range intestazioni {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Result()
}

func TestJSONCompresso(t *testing.T) {
	resp := chiedi(t, server(), "/api/board?from=1715", map[string]string{"Accept-Encoding": "gzip"})
	if resp.StatusCode != 200 {
		t.Fatalf("stato %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("risposta non compressa: %v", resp.Header)
	}
	// Deve essere gzip valido: un corpo compresso a metà passerebbe comunque
	// il controllo sull'intestazione.
	z, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	corpo, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if len(corpo) == 0 {
		t.Fatal("corpo vuoto")
	}
}

// Comprimere un PNG non guadagna niente e complica la risposta: la
// compressione deve valere solo per i tipi testuali.
func TestBinariNonCompressi(t *testing.T) {
	resp := chiedi(t, server(), "/icona-180.png", map[string]string{"Accept-Encoding": "gzip"})
	if resp.Header.Get("Content-Encoding") == "gzip" {
		t.Error("il PNG è stato compresso")
	}
	if resp.StatusCode != 200 {
		t.Errorf("stato %d", resp.StatusCode)
	}
}

// Il client si aggiorna ogni minuto ma il tabellone cambia più di rado: la
// 304 è ciò che rende quasi gratuito quel giro.
func TestNonModificato(t *testing.T) {
	h := server()
	primo := chiedi(t, h, "/api/board?from=1715", nil)
	tag := primo.Header.Get("ETag")
	if tag == "" {
		t.Fatal("nessun ETag")
	}
	secondo := chiedi(t, h, "/api/board?from=1715", map[string]string{"If-None-Match": tag})
	if secondo.StatusCode != http.StatusNotModified {
		t.Fatalf("stato %d, attesa 304", secondo.StatusCode)
	}
	corpo, _ := io.ReadAll(secondo.Body)
	if len(corpo) != 0 {
		t.Errorf("la 304 ha un corpo di %d byte", len(corpo))
	}
	// Anche con gzip richiesto, una 304 non deve dichiararsi compressa.
	terzo := chiedi(t, h, "/api/board?from=1715",
		map[string]string{"If-None-Match": tag, "Accept-Encoding": "gzip"})
	if terzo.StatusCode != http.StatusNotModified {
		t.Fatalf("con gzip: stato %d, attesa 304", terzo.StatusCode)
	}
	if terzo.Header.Get("Content-Encoding") != "" {
		t.Error("la 304 dichiara una codifica")
	}
}

func TestParametriNonValidi(t *testing.T) {
	h := server()
	for _, p := range []string{"/api/board", "/api/board?from=abc", "/api/board?from=1715&to=-3"} {
		if resp := chiedi(t, h, p, nil); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: stato %d, attesa 400", p, resp.StatusCode)
		}
	}
}

func TestElencoStazioni(t *testing.T) {
	resp := chiedi(t, server(), "/api/stations", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("stato %d", resp.StatusCode)
	}
	corpo, _ := io.ReadAll(resp.Body)
	if len(corpo) < 10000 {
		t.Errorf("elenco di soli %d byte", len(corpo))
	}
}
