package main

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

const (
	defaultKioskResetDelay     = 250 * time.Millisecond
	defaultKioskResetTimeout   = 15 * time.Second
	defaultKioskURL            = "https://10.100.0.3"
	defaultKioskBrowser        = "chromium-browser"
	defaultKioskBrowserProfile = "/tmp/valet-kiosk-browser"
	defaultKioskBrowserLog     = "/tmp/valet-kiosk-browser.log"

	kioskWatchdogInterval = 2500 * time.Millisecond
	kioskCookieName       = "valet_kiosk"
	kioskBootstrapPath    = "/kiosk/start"
	kioskTokenQuery       = "token"
)

//go:embed scripts/valet-kiosk-reset.sh
var embeddedKioskResetScript string

type kioskConfig struct {
	Enabled        bool
	ResetDelay     time.Duration
	ResetTimeout   time.Duration
	URL            string
	Browser        string
	BrowserProfile string
	BrowserLog     string
	CookieToken    string
	TLSCertSPKI    string
}

var runKioskResetFunc = runKioskReset

func initializeKioskAuth() error {
	if !conf.Kiosk.Enabled {
		return nil
	}
	token, err := randomState()
	if err != nil {
		return err
	}
	conf.Kiosk.CookieToken = token
	return nil
}

func handleKioskBootstrap(w http.ResponseWriter, r *http.Request) {
	cfg := conf.Kiosk
	if !cfg.Enabled || !tokensEqual(r.URL.Query().Get(kioskTokenQuery), cfg.CookieToken) {
		http.NotFound(w, r)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     kioskCookieName,
		Value:    cfg.CookieToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, kioskRedirectTarget(cfg.URL), http.StatusSeeOther)
}

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

// startKioskWatchdog polls every kioskWatchdogInterval and, whenever the kiosk
// browser is not running, schedules a reset to relaunch it. It returns
// immediately; the polling loop runs in its own goroutine until ctx is done.
func startKioskWatchdog(ctx context.Context) {
	cfg := conf.Kiosk
	if !cfg.Enabled {
		return
	}

	log.Printf("starting kiosk watchdog (interval %s)", kioskWatchdogInterval)
	go func() {
		ticker := time.NewTicker(kioskWatchdogInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if kioskBrowserRunning(ctx, cfg) {
					continue
				}
				scheduleKioskReset(cfg, "watchdog: browser not running")
			}
		}
	}()
}

// kioskBrowserRunning reports whether the kiosk browser process is alive. It
// matches on the browser executable (default chromium-browser) via pgrep.
func kioskBrowserRunning(ctx context.Context, cfg kioskConfig) bool {
	browser := cfg.Browser
	if browser == "" {
		browser = defaultKioskBrowser
	}
	// pgrep exits 0 when at least one process matches, 1 when none do.
	err := exec.CommandContext(ctx, "pgrep", "-f", "--", browser).Run()
	return err == nil
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
	if !c.requestHasKioskCookie(r) {
		return errors.New("request is not from an authenticated kiosk browser")
	}
	return nil
}

func (c kioskConfig) requestHasKioskCookie(r *http.Request) bool {
	cookie, err := r.Cookie(kioskCookieName)
	return err == nil && tokensEqual(cookie.Value, c.CookieToken)
}

func tokensEqual(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func runKioskReset(ctx context.Context, cfg kioskConfig) error {
	launchURL, err := kioskLaunchURL(cfg)
	if err != nil {
		return err
	}
	args := []string{cfg.BrowserProfile, launchURL, cfg.Browser, cfg.BrowserLog, cfg.TLSCertSPKI}
	cmd := exec.CommandContext(ctx, "sh", append([]string{"-s", "--"}, args...)...)
	cmd.Stdin = strings.NewReader(embeddedKioskResetScript)
	return runEmbeddedKioskReset(cmd)
}

func kioskLaunchURL(cfg kioskConfig) (string, error) {
	if cfg.CookieToken == "" {
		return "", errors.New("kiosk cookie token is not initialized")
	}
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return "", fmt.Errorf("parse kiosk URL: %w", err)
	}
	if !u.IsAbs() || u.Host == "" {
		return "", errors.New("kiosk URL must be absolute")
	}
	u.Path = kioskBootstrapPath
	u.RawPath = ""
	u.RawQuery = url.Values{kioskTokenQuery: {cfg.CookieToken}}.Encode()
	u.Fragment = ""
	return u.String(), nil
}

func kioskRedirectTarget(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "/"
	}
	u.Scheme = ""
	u.Host = ""
	u.User = nil
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String()
}

func runEmbeddedKioskReset(cmd *exec.Cmd) error {
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		log.Printf("kiosk reset command output: %s", strings.TrimSpace(string(output)))
	}
	return err
}
