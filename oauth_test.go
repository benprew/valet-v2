package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInferOAuthRedirectURLUsesLoginComplete(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/account", nil)

	got := inferOAuthRedirectURL(request)
	want := "http://localhost:8080/login/complete"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestOAuthAuthorizeURLIncludesRequiredParameters(t *testing.T) {
	cfg := oauthConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		AuthorizeURL: "https://www.recurse.com/oauth/authorize",
		RedirectURL:  "http://localhost:8080/login/complete",
		Scope:        "hub_visits",
	}

	u, err := cfg.authorizeURL("state-value", "claimed@example.com")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(u, "https://www.recurse.com/oauth/authorize?") {
		t.Fatalf("unexpected authorize URL: %s", u)
	}
	for _, expected := range []string{
		"client_id=client-id",
		"redirect_uri=http%3A%2F%2Flocalhost%3A8080%2Flogin%2Fcomplete",
		"login_hint=claimed%40example.com",
		"prompt=login",
		"response_type=code",
		"scope=hub_visits",
		"state=state-value",
	} {
		if !strings.Contains(u, expected) {
			t.Fatalf("authorize URL missing %s: %s", expected, u)
		}
	}
}

func TestOAuthConfigUsesRCBaseURL(t *testing.T) {
	setTestConfig(t, func(c *appConfig) {
		c.RCBaseURL = "https://rc.example.test/"
	})

	cfg := oauthConfigFromRequest(nil)

	if cfg.AuthorizeURL != "https://rc.example.test/oauth/authorize" {
		t.Fatalf("unexpected authorize URL: %q", cfg.AuthorizeURL)
	}
	if cfg.TokenURL != "https://rc.example.test/oauth/token" {
		t.Fatalf("unexpected token URL: %q", cfg.TokenURL)
	}
}

func TestOAuthExchangeCodeReturnsToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		for key, expected := range map[string]string{
			"grant_type":    "authorization_code",
			"code":          "auth-code",
			"client_id":     "client-id",
			"client_secret": "client-secret",
			"redirect_uri":  "http://localhost:8080/login/complete",
		} {
			if got := r.FormValue(key); got != expected {
				t.Fatalf("expected %s=%q, got %q", key, expected, got)
			}
		}

		if err := json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-token",
			"token_type":    "bearer",
			"refresh_token": "refresh-token",
			"scope":         "hub_visits",
			"expires_in":    3600,
		}); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	cfg := oauthConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		TokenURL:     server.URL,
		RedirectURL:  "http://localhost:8080/login/complete",
	}

	token, err := cfg.exchangeCode(context.Background(), "auth-code")
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "access-token" {
		t.Fatalf("unexpected access token: %q", token.AccessToken)
	}
	if token.RefreshToken != "refresh-token" {
		t.Fatalf("unexpected refresh token: %q", token.RefreshToken)
	}
	if token.ExpiresAt == "" {
		t.Fatal("expected ExpiresAt to be set")
	}
}

func TestOAuthRefreshTokenReturnsUpdatedToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		for key, expected := range map[string]string{
			"grant_type":    "refresh_token",
			"refresh_token": "old-refresh-token",
			"client_id":     "client-id",
			"client_secret": "client-secret",
		} {
			if got := r.FormValue(key); got != expected {
				t.Fatalf("expected %s=%q, got %q", key, expected, got)
			}
		}

		if err := json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new-access-token",
			"token_type":   "bearer",
			"expires_in":   3600,
		}); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	cfg := oauthConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		TokenURL:     server.URL,
	}
	current := oauthToken{
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
		Scope:        "hub_visits",
	}

	token, err := cfg.refreshToken(context.Background(), current)
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "new-access-token" {
		t.Fatalf("unexpected access token: %q", token.AccessToken)
	}
	if token.RefreshToken != "old-refresh-token" {
		t.Fatalf("expected refresh token to be preserved, got %q", token.RefreshToken)
	}
	if token.Scope != "hub_visits" {
		t.Fatalf("expected scope to be preserved, got %q", token.Scope)
	}
	if token.ExpiresAt == "" {
		t.Fatal("expected ExpiresAt to be set")
	}
}
