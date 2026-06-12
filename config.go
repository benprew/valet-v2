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
}

var conf = defaultConfig()

func defaultConfig() appConfig {
	return appConfig{
		Addr:             defaultAddr,
		DataPath:         filepath.Join("data", "accounts.db"),
		RCBaseURL:        defaultRCBaseURL,
		HubCheckInterval: defaultHubCheckInterval,
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

	flag.Parse()
}

func rcBaseURL() string {
	baseURL := strings.TrimRight(conf.RCBaseURL, "/")
	if baseURL == "" {
		return defaultRCBaseURL
	}
	return baseURL
}
