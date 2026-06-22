package main

import (
	"context"
	"log"
	"net/http"
	"time"
)

const (
	httpAddr  = ":80"
	httpsAddr = ":443"
)

func main() {
	parseFlags()
	if err := initializeKioskAuth(); err != nil {
		log.Fatalf("initialize kiosk authentication: %v", err)
	}

	tlsConfig, err := loadOrCreateTLSConfig(conf.TLSCertPath, conf.TLSKeyPath)
	if err != nil {
		log.Fatalf("tls setup: %v", err)
	}
	if conf.Kiosk.Enabled {
		conf.Kiosk.TLSCertSPKI, err = tlsCertificateSPKIHash(tlsConfig)
		if err != nil {
			log.Fatalf("prepare kiosk TLS certificate pin: %v", err)
		}
	}

	store, err := openStore(conf.DataPath)
	if err != nil {
		log.Fatalf("open account store: %v", err)
	}
	deviceCache.start(context.Background())
	store.startHubMonitor(context.Background(), currentHubMonitorConfig())

	handler := store.routes()
	scheduleKioskResetOnStartup()
	startKioskWatchdog(context.Background())

	// Every listener serves the same handler. Kiosk resets require the random
	// cookie issued to the browser profile at launch, independent of which
	// listener receives the request.
	errs := make(chan error, 2)

	httpServer := newServer(httpAddr, handler)
	log.Printf("V.A.L.E.T. listening on http://%s", httpAddr)
	go func() { errs <- httpServer.ListenAndServe() }()

	httpsServer := newServer(httpsAddr, handler)
	httpsServer.TLSConfig = tlsConfig
	log.Printf("V.A.L.E.T. listening on https://%s", httpsAddr)
	go func() { errs <- httpsServer.ListenAndServeTLS("", "") }()

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
