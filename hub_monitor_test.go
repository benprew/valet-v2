package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestHubMonitorMarksSeenRegisteredMACInHubOncePerDay(t *testing.T) {
	const (
		email = "ben@example.com"
		mac   = "82:00:3b:d0:93:12"
		date  = "2026-06-08"
	)

	patches := 0
	requestErrors := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			requestErrors <- "unexpected authorization header: " + r.Header.Get("Authorization")
			http.Error(w, "bad authorization", http.StatusUnauthorized)
			return
		}

		switch r.URL.Path {
		case "/api/v1/profiles":
			if r.Method != http.MethodGet {
				requestErrors <- "profile lookup used " + r.Method
				http.Error(w, "bad method", http.StatusMethodNotAllowed)
				return
			}
			if r.URL.Query().Get("query") != email {
				requestErrors <- "unexpected profile query: " + r.URL.Query().Get("query")
				http.Error(w, "bad query", http.StatusBadRequest)
				return
			}
			if err := json.NewEncoder(w).Encode([]map[string]int{{"id": 123}}); err != nil {
				requestErrors <- err.Error()
			}
		case "/api/v1/hub_visits/123/" + date:
			if r.Method != http.MethodPatch {
				requestErrors <- "hub visit update used " + r.Method
				http.Error(w, "bad method", http.StatusMethodNotAllowed)
				return
			}
			patches++
			w.WriteHeader(http.StatusNoContent)
		default:
			requestErrors <- "unexpected request: " + r.Method + " " + r.URL.String()
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	restoreScanner := replaceScannerForTest(t, []networkDevice{{IP: "10.0.0.2", MAC: mac}})
	defer restoreScanner()

	store := &accountStore{
		path:         filepath.Join(t.TempDir(), "accounts.db"),
		Accounts:     map[string][]string{email: {mac}},
		HubVisits:    map[string]string{},
		OAuthTokens:  map[string]oauthToken{email: {AccessToken: "test-token"}},
		RCProfileIDs: map[string]string{},
	}
	client := newHubVisitClient(hubMonitorConfig{
		BaseURL: server.URL,
	})

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	if err := store.runHubMonitorOnce(context.Background(), client, now); err != nil {
		t.Fatalf("first monitor run failed: %v", err)
	}
	if err := store.runHubMonitorOnce(context.Background(), client, now); err != nil {
		t.Fatalf("second monitor run failed: %v", err)
	}
	close(requestErrors)
	for requestError := range requestErrors {
		t.Error(requestError)
	}

	if patches != 1 {
		t.Fatalf("expected one hub visit PATCH, got %d", patches)
	}
	if got := store.HubVisits[email]; got != date {
		t.Fatalf("expected hub visit date %q, got %q", date, got)
	}
	if got := store.RCProfileIDs[email]; got != "123" {
		t.Fatalf("expected cached RC profile id 123, got %q", got)
	}
	reloaded, err := openStore(store.path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	if got := reloaded.HubVisits[email]; got != date {
		t.Fatalf("expected persisted hub visit date %q, got %q", date, got)
	}
	if got := reloaded.RCProfileIDs[email]; got != "123" {
		t.Fatalf("expected persisted RC profile id 123, got %q", got)
	}
	if token, ok := reloaded.OAuthTokens[email]; !ok || token.AccessToken != "test-token" {
		t.Fatalf("expected persisted OAuth token, got %#v", token)
	}
}

func TestHubDateUsesNewYorkCalendarDay(t *testing.T) {
	now := time.Date(2026, 6, 9, 3, 30, 0, 0, time.UTC)

	if got := hubDate(now); got != "2026-06-08" {
		t.Fatalf("expected New York hub date 2026-06-08, got %q", got)
	}
}

func TestHubMonitorRefreshesExpiredOAuthToken(t *testing.T) {
	const (
		email = "ben@example.com"
		mac   = "82:00:3b:d0:93:12"
		date  = "2026-06-08"
	)

	tokenRefreshes := 0
	patches := 0
	requestErrors := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			if r.Method != http.MethodPost {
				requestErrors <- "token refresh used " + r.Method
				http.Error(w, "bad method", http.StatusMethodNotAllowed)
				return
			}
			if err := r.ParseForm(); err != nil {
				requestErrors <- err.Error()
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			for key, expected := range map[string]string{
				"grant_type":    "refresh_token",
				"refresh_token": "refresh-token",
				"client_id":     "client-id",
				"client_secret": "client-secret",
			} {
				if got := r.FormValue(key); got != expected {
					requestErrors <- "expected " + key + "=" + expected + ", got " + got
					http.Error(w, "bad token refresh", http.StatusBadRequest)
					return
				}
			}
			tokenRefreshes++
			if err := json.NewEncoder(w).Encode(map[string]any{
				"access_token": "fresh-token",
				"expires_in":   3600,
			}); err != nil {
				requestErrors <- err.Error()
			}
		case "/api/v1/profiles":
			if r.Header.Get("Authorization") != "Bearer fresh-token" {
				requestErrors <- "profile lookup used authorization header: " + r.Header.Get("Authorization")
				http.Error(w, "bad authorization", http.StatusUnauthorized)
				return
			}
			if err := json.NewEncoder(w).Encode([]map[string]int{{"id": 123}}); err != nil {
				requestErrors <- err.Error()
			}
		case "/api/v1/hub_visits/123/" + date:
			if r.Header.Get("Authorization") != "Bearer fresh-token" {
				requestErrors <- "hub visit used authorization header: " + r.Header.Get("Authorization")
				http.Error(w, "bad authorization", http.StatusUnauthorized)
				return
			}
			patches++
			w.WriteHeader(http.StatusNoContent)
		default:
			requestErrors <- "unexpected request: " + r.Method + " " + r.URL.String()
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv("VALET_OAUTH_CLIENT_ID", "client-id")
	t.Setenv("VALET_OAUTH_CLIENT_SECRET", "client-secret")
	t.Setenv("VALET_OAUTH_TOKEN_URL", server.URL+"/oauth/token")

	restoreScanner := replaceScannerForTest(t, []networkDevice{{IP: "10.0.0.2", MAC: mac}})
	defer restoreScanner()

	store := &accountStore{
		path:      filepath.Join(t.TempDir(), "accounts.db"),
		Accounts:  map[string][]string{email: {mac}},
		HubVisits: map[string]string{},
		OAuthTokens: map[string]oauthToken{email: {
			AccessToken:  "expired-token",
			RefreshToken: "refresh-token",
			ExpiresAt:    "2026-06-08T00:00:00Z",
		}},
		RCProfileIDs: map[string]string{},
	}
	client := newHubVisitClient(hubMonitorConfig{
		BaseURL: server.URL,
	})

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	if err := store.runHubMonitorOnce(context.Background(), client, now); err != nil {
		t.Fatalf("monitor run failed: %v", err)
	}
	close(requestErrors)
	for requestError := range requestErrors {
		t.Error(requestError)
	}

	if tokenRefreshes != 1 {
		t.Fatalf("expected one token refresh, got %d", tokenRefreshes)
	}
	if patches != 1 {
		t.Fatalf("expected one hub visit PATCH, got %d", patches)
	}
	token := store.OAuthTokens[email]
	if token.AccessToken != "fresh-token" {
		t.Fatalf("expected refreshed access token to be saved, got %q", token.AccessToken)
	}
	if token.RefreshToken != "refresh-token" {
		t.Fatalf("expected refresh token to be preserved, got %q", token.RefreshToken)
	}
	if token.ExpiresAt == "" || token.ExpiresAt == "2026-06-08T00:00:00Z" {
		t.Fatalf("expected refreshed ExpiresAt, got %q", token.ExpiresAt)
	}
}

func TestHubMonitorConfigUsesRCBaseURL(t *testing.T) {
	t.Setenv("VALET_RC_BASE_URL", "https://rc.example.test/")

	cfg := hubMonitorConfigFromEnv()

	if cfg.BaseURL != "https://rc.example.test" {
		t.Fatalf("unexpected Hub base URL: %q", cfg.BaseURL)
	}
}

func TestHubMonitorSkipsAmbiguousMACAssignments(t *testing.T) {
	const mac = "82:00:3b:d0:93:12"

	patches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			patches++
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	restoreScanner := replaceScannerForTest(t, []networkDevice{{IP: "10.0.0.2", MAC: mac}})
	defer restoreScanner()

	store := &accountStore{
		path: filepath.Join(t.TempDir(), "accounts.db"),
		Accounts: map[string][]string{
			"one@example.com": {mac},
			"two@example.com": {mac},
		},
		HubVisits: map[string]string{},
		OAuthTokens: map[string]oauthToken{
			"one@example.com": {AccessToken: "test-token"},
			"two@example.com": {AccessToken: "test-token"},
		},
		RCProfileIDs: map[string]string{},
	}
	client := newHubVisitClient(hubMonitorConfig{
		BaseURL: server.URL,
	})

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	if err := store.runHubMonitorOnce(context.Background(), client, now); err != nil {
		t.Fatalf("monitor run failed: %v", err)
	}
	if patches != 0 {
		t.Fatalf("expected no hub visit PATCHes for ambiguous MAC, got %d", patches)
	}
}

func TestHubMonitorSkipsMACWithoutOAuthToken(t *testing.T) {
	const (
		email = "ben@example.com"
		mac   = "82:00:3b:d0:93:12"
	)

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	restoreScanner := replaceScannerForTest(t, []networkDevice{{IP: "10.0.0.2", MAC: mac}})
	defer restoreScanner()

	store := &accountStore{
		path:         filepath.Join(t.TempDir(), "accounts.db"),
		Accounts:     map[string][]string{email: {mac}},
		HubVisits:    map[string]string{},
		OAuthTokens:  map[string]oauthToken{},
		RCProfileIDs: map[string]string{},
	}
	client := newHubVisitClient(hubMonitorConfig{
		BaseURL: server.URL,
	})

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	if err := store.runHubMonitorOnce(context.Background(), client, now); err != nil {
		t.Fatalf("monitor run failed: %v", err)
	}
	if requests != 0 {
		t.Fatalf("expected no RC API requests without OAuth token, got %d", requests)
	}
}

func replaceScannerForTest(t *testing.T, devices []networkDevice) func() {
	t.Helper()

	original := scanNetworkDevicesFunc
	scanNetworkDevicesFunc = func(context.Context) ([]networkDevice, error) {
		return devices, nil
	}
	return func() {
		scanNetworkDevicesFunc = original
	}
}
