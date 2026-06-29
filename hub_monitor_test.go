package main

import (
	"context"
	"encoding/json"
	"errors"
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

	restoreScanner := replaceScannerForTest(t, []networkDevice{{IP: "10.0.0.2", MAC: mac, Source: "ip-neigh", State: "REACHABLE"}})
	defer restoreScanner()

	path := filepath.Join(t.TempDir(), "accounts.db")
	store, err := openStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	addAccountMAC(t, store, email, mac)
	if err := store.saveToken(email, token{Type: tokenTypePAT, RefreshToken: "test-token"}); err != nil {
		t.Fatal(err)
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
	if got := store.lastHubVisit(email); got != date {
		t.Fatalf("expected hub visit date %q, got %q", date, got)
	}
	if got, _ := store.rcProfileID(email); got != "123" {
		t.Fatalf("expected cached RC profile id 123, got %q", got)
	}

	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := openStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	if got, _ := reloaded.rcProfileID(email); got != "123" {
		t.Fatalf("expected persisted RC profile id 123, got %q", got)
	}
	if !reloaded.hasToken(email) {
		t.Fatal("expected persisted token")
	}
	if got := reloaded.lastHubVisit(email); got != "" {
		t.Fatalf("expected hub visits to reset on restart, got %q", got)
	}
}

func TestHubDateUsesNewYorkCalendarDay(t *testing.T) {
	now := time.Date(2026, 6, 9, 3, 30, 0, 0, time.UTC)

	if got := hubDate(now); got != "2026-06-08" {
		t.Fatalf("expected New York hub date 2026-06-08, got %q", got)
	}
}

func TestHubMonitorRefreshesOAuthAccessToken(t *testing.T) {
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

	setTestConfig(t, func(c *appConfig) {
		c.OAuthClientID = "client-id"
		c.OAuthClientSecret = "client-secret"
		c.OAuthTokenURL = server.URL + "/oauth/token"
	})

	restoreScanner := replaceScannerForTest(t, []networkDevice{{IP: "10.0.0.2", MAC: mac, Source: "ip-neigh", State: "REACHABLE"}})
	defer restoreScanner()

	store := testStore(t)
	addAccountMAC(t, store, email, mac)
	if err := store.saveToken(email, token{
		Type:         tokenTypeOAuth,
		RefreshToken: "refresh-token",
		ExpiresAt:    "2026-06-08T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
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
	saved, ok, err := store.token(email)
	if err != nil || !ok {
		t.Fatalf("expected stored token, got ok=%v err=%v", ok, err)
	}
	if saved.RefreshToken != "refresh-token" {
		t.Fatalf("expected refresh token to be preserved, got %q", saved.RefreshToken)
	}
	if saved.ExpiresAt == "" || saved.ExpiresAt == "2026-06-08T00:00:00Z" {
		t.Fatalf("expected refreshed ExpiresAt, got %q", saved.ExpiresAt)
	}
}

func TestHubMonitorConfigUsesRCBaseURL(t *testing.T) {
	setTestConfig(t, func(c *appConfig) {
		c.RCBaseURL = "https://rc.example.test/"
		c.HubCheckInterval = 30 * time.Second
		c.HubScanTimeout = 45 * time.Second
	})

	cfg := currentHubMonitorConfig()

	if cfg.BaseURL != "https://rc.example.test" {
		t.Fatalf("unexpected Hub base URL: %q", cfg.BaseURL)
	}
	if cfg.Interval != 30*time.Second {
		t.Fatalf("unexpected Hub check interval: %s", cfg.Interval)
	}
	if cfg.ScanTimeout != 45*time.Second {
		t.Fatalf("unexpected Hub scan timeout: %s", cfg.ScanTimeout)
	}
}

func TestHubMonitorScanUsesConfiguredTimeout(t *testing.T) {
	var gotDeadline time.Time
	restoreScanner := replaceScannerFuncForTest(t, func(ctx context.Context) ([]networkDevice, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("expected scan context deadline")
		}
		gotDeadline = deadline
		return nil, nil
	})
	defer restoreScanner()

	store := testStore(t)
	start := time.Now()
	store.runHubMonitorScan(context.Background(), newHubVisitClient(hubMonitorConfig{}), 30*time.Second)

	timeout := gotDeadline.Sub(start)
	if timeout < 29*time.Second || timeout > 31*time.Second {
		t.Fatalf("expected scan deadline about 30s from start, got %s", timeout)
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

	store := testStore(t)
	for _, email := range []string{"one@example.com", "two@example.com"} {
		addAccountMAC(t, store, email, mac)
		if err := store.saveToken(email, token{Type: tokenTypePAT, RefreshToken: "test-token"}); err != nil {
			t.Fatal(err)
		}
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

func TestHubMonitorSkipsMACWithoutToken(t *testing.T) {
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

	store := testStore(t)
	addAccountMAC(t, store, email, mac)
	client := newHubVisitClient(hubMonitorConfig{
		BaseURL: server.URL,
	})

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	if err := store.runHubMonitorOnce(context.Background(), client, now); err != nil {
		t.Fatalf("monitor run failed: %v", err)
	}
	if requests != 0 {
		t.Fatalf("expected no RC API requests without a token, got %d", requests)
	}
}

func TestHubMonitorVerifiesStaleNeighborBeforeMarking(t *testing.T) {
	const (
		email = "ben@example.com"
		mac   = "82:00:3b:d0:93:12"
		date  = "2026-06-08"
	)

	patches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/profiles":
			_ = json.NewEncoder(w).Encode([]map[string]int{{"id": 123}})
		case "/api/v1/hub_visits/123/" + date:
			patches++
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// A STALE neighbor entry is only a candidate; an ARP probe must confirm it.
	restoreScanner := replaceScannerForTest(t, []networkDevice{{IP: "10.0.0.2", MAC: mac, Source: "ip-neigh", State: "STALE"}})
	defer restoreScanner()

	probed := 0
	restoreVerify := replaceVerifyForTest(t, func(_ context.Context, devices []networkDevice) (bool, error) {
		probed++
		if len(devices) != 1 || devices[0].IP != "10.0.0.2" {
			t.Errorf("verify got unexpected devices: %#v", devices)
		}
		return true, nil
	})
	defer restoreVerify()

	store := testStore(t)
	addAccountMAC(t, store, email, mac)
	if err := store.saveToken(email, token{Type: tokenTypePAT, RefreshToken: "test-token"}); err != nil {
		t.Fatal(err)
	}
	client := newHubVisitClient(hubMonitorConfig{BaseURL: server.URL})

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	if err := store.runHubMonitorOnce(context.Background(), client, now); err != nil {
		t.Fatalf("monitor run failed: %v", err)
	}
	if probed != 1 {
		t.Fatalf("expected one ARP verification, got %d", probed)
	}
	if patches != 1 {
		t.Fatalf("expected one hub visit PATCH after verification, got %d", patches)
	}
}

func TestHubMonitorSkipsStaleNeighborThatDoesNotAnswerProbe(t *testing.T) {
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

	restoreScanner := replaceScannerForTest(t, []networkDevice{{IP: "10.0.0.2", MAC: mac, Source: "ip-neigh", State: "STALE"}})
	defer restoreScanner()

	restoreVerify := replaceVerifyForTest(t, func(context.Context, []networkDevice) (bool, error) {
		return false, nil // device has left; the STALE entry is a ghost
	})
	defer restoreVerify()

	store := testStore(t)
	addAccountMAC(t, store, email, mac)
	if err := store.saveToken(email, token{Type: tokenTypePAT, RefreshToken: "test-token"}); err != nil {
		t.Fatal(err)
	}
	client := newHubVisitClient(hubMonitorConfig{BaseURL: server.URL})

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	if err := store.runHubMonitorOnce(context.Background(), client, now); err != nil {
		t.Fatalf("monitor run failed: %v", err)
	}
	if requests != 0 {
		t.Fatalf("expected no RC API requests when probe fails, got %d", requests)
	}
	if got := store.lastHubVisit(email); got != "" {
		t.Fatalf("expected no hub visit recorded, got %q", got)
	}
}

func TestHubMonitorFailsClosedWhenProbeErrors(t *testing.T) {
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

	restoreScanner := replaceScannerForTest(t, []networkDevice{{IP: "10.0.0.2", MAC: mac, Source: "ip-neigh", State: "STALE"}})
	defer restoreScanner()

	restoreVerify := replaceVerifyForTest(t, func(context.Context, []networkDevice) (bool, error) {
		return false, errors.New("arp-scan unavailable")
	})
	defer restoreVerify()

	store := testStore(t)
	addAccountMAC(t, store, email, mac)
	if err := store.saveToken(email, token{Type: tokenTypePAT, RefreshToken: "test-token"}); err != nil {
		t.Fatal(err)
	}
	client := newHubVisitClient(hubMonitorConfig{BaseURL: server.URL})

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	err := store.runHubMonitorOnce(context.Background(), client, now)
	if err == nil {
		t.Fatal("expected probe error to surface from monitor run")
	}
	if requests != 0 {
		t.Fatalf("expected no RC API requests when probe errors, got %d", requests)
	}
	if got := store.lastHubVisit(email); got != "" {
		t.Fatalf("expected no hub visit recorded, got %q", got)
	}
}

func TestHubMonitorSkipsProbeForReachableNeighbor(t *testing.T) {
	const (
		email = "ben@example.com"
		mac   = "82:00:3b:d0:93:12"
		date  = "2026-06-08"
	)

	patches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/profiles":
			_ = json.NewEncoder(w).Encode([]map[string]int{{"id": 123}})
		case "/api/v1/hub_visits/123/" + date:
			patches++
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	restoreScanner := replaceScannerForTest(t, []networkDevice{{IP: "10.0.0.2", MAC: mac, Source: "ip-neigh", State: "REACHABLE"}})
	defer restoreScanner()

	restoreVerify := replaceVerifyForTest(t, func(context.Context, []networkDevice) (bool, error) {
		t.Fatal("REACHABLE neighbor should not be ARP-probed")
		return false, nil
	})
	defer restoreVerify()

	store := testStore(t)
	addAccountMAC(t, store, email, mac)
	if err := store.saveToken(email, token{Type: tokenTypePAT, RefreshToken: "test-token"}); err != nil {
		t.Fatal(err)
	}
	client := newHubVisitClient(hubMonitorConfig{BaseURL: server.URL})

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	if err := store.runHubMonitorOnce(context.Background(), client, now); err != nil {
		t.Fatalf("monitor run failed: %v", err)
	}
	if patches != 1 {
		t.Fatalf("expected one hub visit PATCH for reachable neighbor, got %d", patches)
	}
}

func replaceVerifyForTest(t *testing.T, replacement func(context.Context, []networkDevice) (bool, error)) func() {
	t.Helper()

	original := verifyDevicePresentFunc
	verifyDevicePresentFunc = replacement
	return func() {
		verifyDevicePresentFunc = original
	}
}

func replaceScannerForTest(t *testing.T, devices []networkDevice) func() {
	t.Helper()

	return replaceScannerFuncForTest(t, func(context.Context) ([]networkDevice, error) {
		return devices, nil
	})
}

func replaceScannerFuncForTest(t *testing.T, replacement func(context.Context) ([]networkDevice, error)) func() {
	t.Helper()

	originalScan := scanNetworkDevicesFunc
	originalCached := cachedNetworkDevicesFunc
	scanNetworkDevicesFunc = replacement
	cachedNetworkDevicesFunc = replacement
	return func() {
		scanNetworkDevicesFunc = originalScan
		cachedNetworkDevicesFunc = originalCached
	}
}
