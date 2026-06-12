package main

import (
	"context"
	_ "embed"
	"errors"
	"log"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

const (
	defaultKioskResetDelay     = 250 * time.Millisecond
	defaultKioskResetTimeout   = 15 * time.Second
	defaultKioskURL            = "http://127.0.0.1:3000"
	defaultKioskBrowser        = "chromium-browser"
	defaultKioskBrowserProfile = "/tmp/valet-kiosk-browser"
	defaultKioskBrowserLog     = "/tmp/valet-kiosk-browser.log"
)

//go:embed scripts/valet-kiosk-reset.sh
var embeddedKioskResetScript string

type kioskConfig struct {
	Enabled        bool
	ResetCommand   string
	ResetDelay     time.Duration
	ResetTimeout   time.Duration
	URL            string
	Browser        string
	BrowserProfile string
	BrowserLog     string
}

var runKioskResetFunc = runKioskReset

func scheduleKioskResetAfterResponse(r *http.Request, reason string) {
	cfg := conf.Kiosk
	if err := cfg.validateResetRequest(r); err != nil {
		if cfg.Enabled {
			log.Printf("kiosk reset skipped for %s: %v", reason, err)
		}
		return
	}

	scheduleKioskReset(cfg, "after "+reason)
}

func scheduleKioskResetOnStartup() {
	cfg := conf.Kiosk
	if !cfg.Enabled {
		return
	}

	scheduleKioskReset(cfg, "at startup")
}

func scheduleKioskReset(cfg kioskConfig, reason string) {
	log.Printf("scheduling kiosk reset %s", reason)
	go func() {
		if cfg.ResetDelay > 0 {
			time.Sleep(cfg.ResetDelay)
		}

		ctx, cancel := context.WithTimeout(context.Background(), cfg.ResetTimeout)
		defer cancel()
		if err := runKioskResetFunc(ctx, cfg); err != nil {
			log.Printf("kiosk reset %s failed: %v", reason, err)
		}
	}()
}

func (c kioskConfig) validateResetRequest(r *http.Request) error {
	if !c.Enabled {
		return errors.New("kiosk mode is disabled")
	}
	if !requestIsFromLoopback(r) {
		return errors.New("request is not from loopback")
	}
	return nil
}

func requestIsFromLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// The reset script receives the browser settings as positional arguments:
// $1 profile directory, $2 URL, $3 browser executable, $4 browser log file.
// A custom -kiosk-reset-command gets the same arguments.
func runKioskReset(ctx context.Context, cfg kioskConfig) error {
	args := []string{cfg.BrowserProfile, cfg.URL, cfg.Browser, cfg.BrowserLog}
	if cfg.ResetCommand != "" {
		return runKioskResetCommand(exec.CommandContext(ctx, "sh", append([]string{"-c", cfg.ResetCommand, "valet-kiosk-reset"}, args...)...))
	}

	cmd := exec.CommandContext(ctx, "sh", append([]string{"-s", "--"}, args...)...)
	cmd.Stdin = strings.NewReader(embeddedKioskResetScript)
	return runKioskResetCommand(cmd)
}

func runKioskResetCommand(cmd *exec.Cmd) error {
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		log.Printf("kiosk reset command output: %s", strings.TrimSpace(string(output)))
	}
	return err
}
