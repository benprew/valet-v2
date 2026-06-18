package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestKioskResetRequiresLoopbackRequest(t *testing.T) {
	cfg := kioskConfig{Enabled: true}

	local := httptest.NewRequest(http.MethodPost, "/logout", nil)
	local.RemoteAddr = "127.0.0.1:12345"
	if err := cfg.validateResetRequest(local); err != nil {
		t.Fatalf("expected local request to be allowed, got %v", err)
	}

	remote := httptest.NewRequest(http.MethodPost, "/logout", nil)
	remote.RemoteAddr = "192.0.2.1:12345"
	if err := cfg.validateResetRequest(remote); err == nil {
		t.Fatal("expected remote request to be rejected")
	}
}

func TestKioskResetRequiresMode(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/logout", nil)
	request.RemoteAddr = "127.0.0.1:12345"

	if err := (kioskConfig{}).validateResetRequest(request); err == nil {
		t.Fatal("expected disabled kiosk mode to be rejected")
	}
}

func TestEmbeddedKioskResetScriptRunsWithoutExternalPath(t *testing.T) {
	browser, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	profileDir := filepath.Join(dir, "profile")
	cfg := kioskConfig{
		Browser:        browser,
		BrowserProfile: profileDir,
		BrowserLog:     filepath.Join(dir, "browser.log"),
		URL:            "http://127.0.0.1:3000",
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runKioskReset(ctx, cfg); err != nil {
		t.Fatalf("run embedded kiosk reset: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		matches, err := filepath.Glob(profileDir + ".*")
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected temporary profile directories to be cleaned up, found %#v", matches)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStartupTriggersEmbeddedKioskResetWhenKioskModeEnabled(t *testing.T) {
	setTestConfig(t, func(c *appConfig) {
		c.Kiosk.Enabled = true
		c.Kiosk.ResetDelay = 0
	})

	resetConfigs := make(chan kioskConfig, 1)
	restoreRunner := replaceKioskResetRunnerForTest(func(_ context.Context, cfg kioskConfig) error {
		resetConfigs <- cfg
		return nil
	})
	defer restoreRunner()

	scheduleKioskResetOnStartup()

	select {
	case cfg := <-resetConfigs:
		if !cfg.Enabled {
			t.Fatal("expected kiosk mode to be enabled")
		}
		if cfg.ResetCommand != "" {
			t.Fatalf("expected embedded reset script, got command %q", cfg.ResetCommand)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for startup kiosk reset")
	}
}

func TestStartupDoesNotTriggerKioskResetWhenKioskModeDisabled(t *testing.T) {
	setTestConfig(t, func(c *appConfig) {
		c.Kiosk.Enabled = false
		c.Kiosk.ResetDelay = 0
	})

	resetConfigs := make(chan kioskConfig, 1)
	restoreRunner := replaceKioskResetRunnerForTest(func(_ context.Context, cfg kioskConfig) error {
		resetConfigs <- cfg
		return nil
	})
	defer restoreRunner()

	scheduleKioskResetOnStartup()

	select {
	case cfg := <-resetConfigs:
		t.Fatalf("unexpected startup kiosk reset with config %#v", cfg)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestLogoutTriggersEmbeddedKioskResetForLocalKioskRequest(t *testing.T) {
	setTestConfig(t, func(c *appConfig) {
		c.Kiosk.Enabled = true
		c.Kiosk.ResetDelay = 0
	})

	resetConfigs := make(chan kioskConfig, 1)
	restoreRunner := replaceKioskResetRunnerForTest(func(_ context.Context, cfg kioskConfig) error {
		resetConfigs <- cfg
		return nil
	})
	defer restoreRunner()

	const email = "ben@example.com"
	store := testStore(t)
	handler := store.routes()

	loginResponse := httptest.NewRecorder()
	loginRequest := formRequest(http.MethodPost, "/login", url.Values{"email": {email}})
	loginRequest.RemoteAddr = "127.0.0.1:12345"
	handler.ServeHTTP(loginResponse, loginRequest)

	cookie := sessionCookie(t, loginResponse.Result())
	current := storedSession(t, store, cookie.Value)

	logoutResponse := httptest.NewRecorder()
	logoutRequest := formRequest(http.MethodPost, "/logout", url.Values{csrfFormField: {current.CSRFToken}})
	logoutRequest.RemoteAddr = "127.0.0.1:12345"
	logoutRequest.AddCookie(cookie)
	handler.ServeHTTP(logoutResponse, logoutRequest)

	if logoutResponse.Code != http.StatusSeeOther {
		t.Fatalf("expected logout redirect, got %d", logoutResponse.Code)
	}
	select {
	case cfg := <-resetConfigs:
		if cfg.ResetCommand != "" {
			t.Fatalf("expected embedded reset script, got command %q", cfg.ResetCommand)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for kiosk reset")
	}
}

func TestLogoutDoesNotTriggerKioskResetForRemoteRequest(t *testing.T) {
	setTestConfig(t, func(c *appConfig) {
		c.Kiosk.Enabled = true
		c.Kiosk.ResetDelay = 0
	})

	resetConfigs := make(chan kioskConfig, 1)
	restoreRunner := replaceKioskResetRunnerForTest(func(_ context.Context, cfg kioskConfig) error {
		resetConfigs <- cfg
		return nil
	})
	defer restoreRunner()

	const email = "ben@example.com"
	store := testStore(t)
	handler := store.routes()

	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, formRequest(http.MethodPost, "/login", url.Values{"email": {email}}))

	cookie := sessionCookie(t, loginResponse.Result())
	current := storedSession(t, store, cookie.Value)

	logoutResponse := httptest.NewRecorder()
	logoutRequest := formRequest(http.MethodPost, "/logout", url.Values{csrfFormField: {current.CSRFToken}})
	logoutRequest.RemoteAddr = "192.0.2.1:12345"
	logoutRequest.AddCookie(cookie)
	handler.ServeHTTP(logoutResponse, logoutRequest)

	if logoutResponse.Code != http.StatusSeeOther {
		t.Fatalf("expected logout redirect, got %d", logoutResponse.Code)
	}
	select {
	case cfg := <-resetConfigs:
		t.Fatalf("unexpected kiosk reset with config %#v", cfg)
	default:
	}
}

func TestOAuthMismatchTriggersKioskResetForLocalKioskRequest(t *testing.T) {
	const (
		claimedEmail       = "claimed@example.com"
		authenticatedEmail = "actual@example.com"
	)

	setTestConfig(t, func(c *appConfig) {
		c.Kiosk.Enabled = true
		c.Kiosk.ResetDelay = 0
	})

	resetConfigs := make(chan kioskConfig, 1)
	restoreRunner := replaceKioskResetRunnerForTest(func(_ context.Context, cfg kioskConfig) error {
		resetConfigs <- cfg
		return nil
	})
	defer restoreRunner()

	restoreScanner := replaceScannerForTest(t, nil)
	defer restoreScanner()

	store := testStore(t)
	state, err := store.newOAuthState(claimedEmail, "http://localhost:8080/login/complete")
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			if err := json.NewEncoder(w).Encode(map[string]string{
				"access_token": "actual-token",
				"token_type":   "bearer",
			}); err != nil {
				t.Fatal(err)
			}
		case "/api/v1/profiles/me":
			if err := json.NewEncoder(w).Encode(map[string]string{
				"id":    "123",
				"email": authenticatedEmail,
			}); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	setTestConfig(t, func(c *appConfig) {
		c.OAuthClientID = "client-id"
		c.OAuthClientSecret = "client-secret"
		c.OAuthTokenURL = server.URL + "/oauth/token"
		c.RCBaseURL = server.URL
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/login/complete?state="+url.QueryEscape(state)+"&code=auth-code", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	store.routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected mismatch page, got %d", response.Code)
	}
	select {
	case cfg := <-resetConfigs:
		if cfg.ResetCommand != "" {
			t.Fatalf("expected embedded reset script, got command %q", cfg.ResetCommand)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for kiosk reset")
	}
}

func replaceKioskResetRunnerForTest(replacement func(context.Context, kioskConfig) error) func() {
	original := runKioskResetFunc
	runKioskResetFunc = replacement
	return func() {
		runKioskResetFunc = original
	}
}
