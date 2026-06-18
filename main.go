package main

import (
	"context"
	"log"
	"net/http"
	"time"
)

const defaultAddr = "127.0.0.1:3000"

func main() {
	parseFlags()

	store, err := openStore(conf.DataPath)
	if err != nil {
		log.Fatalf("open account store: %v", err)
	}
	deviceCache.start(context.Background())
	store.startHubMonitor(context.Background(), currentHubMonitorConfig())

	handler := store.routes()
	scheduleKioskResetOnStartup()
	startKioskWatchdog(context.Background())

	// Every listener serves the same handler. Kiosk resets are gated on the
	// connection's remote address (see requestIsFromLoopback), so only requests
	// arriving over the loopback listener can trigger a browser reset; LAN
	// requests on -http-addr/-https-addr cannot.
	errs := make(chan error, 3)
	started := 0

	for _, addr := range dedupe(conf.Addr, conf.HTTPAddr) {
		srv := newServer(addr, handler)
		started++
		log.Printf("V.A.L.E.T. listening on http://%s", addr)
		go func() { errs <- srv.ListenAndServe() }()
	}

	if conf.HTTPSAddr != "" {
		tlsConfig, err := loadOrCreateTLSConfig(conf.TLSCertPath, conf.TLSKeyPath)
		if err != nil {
			log.Fatalf("tls setup: %v", err)
		}
		srv := newServer(conf.HTTPSAddr, handler)
		srv.TLSConfig = tlsConfig
		started++
		log.Printf("V.A.L.E.T. listening on https://%s", conf.HTTPSAddr)
		go func() { errs <- srv.ListenAndServeTLS("", "") }()
	}

	if started == 0 {
		log.Fatal("no listen addresses configured; set -addr, -http-addr, or -https-addr")
	}

	log.Fatal(<-errs)
}

func newServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

// dedupe returns the non-empty, distinct addresses in order so we never try to
// bind the same address twice (e.g. when -addr and -http-addr coincide).
func dedupe(addrs ...string) []string {
	seen := make(map[string]bool, len(addrs))
	var out []string
	for _, a := range addrs {
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	return out
}
