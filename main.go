package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"
)

const defaultAddr = "127.0.0.1:8080"

func main() {
	store, err := openStore(dataPath())
	if err != nil {
		log.Fatalf("open account store: %v", err)
	}
	store.startHubMonitor(context.Background(), hubMonitorConfigFromEnv())

	addr := os.Getenv("VALET_ADDR")
	if addr == "" {
		addr = defaultAddr
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           store.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("V.A.L.E.T. listening on http://%s", addr)
	log.Fatal(server.ListenAndServe())
}
