package main

import (
	"flag"
	"path/filepath"
	"strings"
	"time"
)

const defaultRCBaseURL = "https://www.recurse.com"

type appConfig struct {
	Addr      string
	DataPath  string
	RCBaseURL string

	OAuthClientID     string
	OAuthClientSecret string
	OAuthAuthorizeURL string
	OAuthTokenURL     string
	OAuthRedirectURL  string
	OAuthScope        string

	HubCheckInterval time.Duration
	HubScanTimeout   time.Duration

	Kiosk kioskConfig
}

var conf = defaultConfig()

func defaultConfig() appConfig {
	return appConfig{
		Addr:             defaultAddr,
		DataPath:         filepath.Join("data", "accounts.db"),
		RCBaseURL:        defaultRCBaseURL,
		HubCheckInterval: defaultHubCheckInterval,
		HubScanTimeout:   defaultHubScanTimeout,
		Kiosk: kioskConfig{
			ResetDelay:     defaultKioskResetDelay,
			ResetTimeout:   defaultKioskResetTimeout,
			URL:            defaultKioskURL,
			Browser:        defaultKioskBrowser,
			BrowserProfile: defaultKioskBrowserProfile,
			BrowserLog:     defaultKioskBrowserLog,
		},
	}
}

func parseFlags() {
	flag.StringVar(&conf.Addr, "addr", conf.Addr, "address to listen on")
	flag.StringVar(&conf.DataPath, "data", conf.DataPath, "path to the SQLite data file")
	flag.StringVar(&conf.RCBaseURL, "rc-base-url", conf.RCBaseURL, "base URL for Recurse Center OAuth and API requests")

	flag.StringVar(&conf.OAuthClientID, "oauth-client-id", "", "OAuth client ID")
	flag.StringVar(&conf.OAuthClientSecret, "oauth-client-secret", "", "OAuth client secret")
	flag.StringVar(&conf.OAuthAuthorizeURL, "oauth-authorize-url", "", "OAuth authorize URL (default <rc-base-url>/oauth/authorize)")
	flag.StringVar(&conf.OAuthTokenURL, "oauth-token-url", "", "OAuth token URL (default <rc-base-url>/oauth/token)")
	flag.StringVar(&conf.OAuthRedirectURL, "oauth-redirect-url", "", "OAuth redirect URL (default inferred from the request host)")
	flag.StringVar(&conf.OAuthScope, "oauth-scope", "", "OAuth scope to request")

	flag.DurationVar(&conf.HubCheckInterval, "hub-check-interval", conf.HubCheckInterval, "how often the hub monitor scans local devices")
	flag.DurationVar(&conf.HubScanTimeout, "hub-scan-timeout", conf.HubScanTimeout, "timeout for a local device scan")

	flag.BoolVar(&conf.Kiosk.Enabled, "kiosk", false, "enable kiosk mode")
	flag.StringVar(&conf.Kiosk.ResetCommand, "kiosk-reset-command", "", "shell command that overrides the embedded kiosk reset script")
	flag.DurationVar(&conf.Kiosk.ResetDelay, "kiosk-reset-delay", conf.Kiosk.ResetDelay, "delay before running a kiosk reset")
	flag.DurationVar(&conf.Kiosk.ResetTimeout, "kiosk-reset-timeout", conf.Kiosk.ResetTimeout, "timeout for the kiosk reset command")
	flag.StringVar(&conf.Kiosk.URL, "kiosk-url", conf.Kiosk.URL, "URL the kiosk browser opens")
	flag.StringVar(&conf.Kiosk.Browser, "kiosk-browser", conf.Kiosk.Browser, "browser executable used in kiosk mode")
	flag.StringVar(&conf.Kiosk.BrowserProfile, "kiosk-browser-profile", conf.Kiosk.BrowserProfile, "browser profile directory used in kiosk mode")
	flag.StringVar(&conf.Kiosk.BrowserLog, "kiosk-browser-log", conf.Kiosk.BrowserLog, "log file for the kiosk browser")

	flag.Parse()
}

func rcBaseURL() string {
	baseURL := strings.TrimRight(conf.RCBaseURL, "/")
	if baseURL == "" {
		return defaultRCBaseURL
	}
	return baseURL
}
