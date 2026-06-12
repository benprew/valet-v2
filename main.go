package main

import (
	"context"
	"log"
	"net/http"
	"time"
)

const defaultAddr = "127.0.0.1:8080"

func main() {
	parseFlags()

	store, err := openStore(conf.DataPath)
	if err != nil {
		log.Fatalf("open account store: %v", err)
	}
	store.startHubMonitor(context.Background(), currentHubMonitorConfig())

	server := &http.Server{
		Addr:              conf.Addr,
		Handler:           store.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("V.A.L.E.T. listening on http://%s", conf.Addr)
	log.Fatal(server.ListenAndServe())
}
