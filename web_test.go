package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultAddrBindsLocalhost(t *testing.T) {
	if defaultAddr != "127.0.0.1:8080" {
		t.Fatalf("defaultAddr should bind localhost, got %q", defaultAddr)
	}
}

func TestAccountRequiresSession(t *testing.T) {
	store := testStore(t)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/account", nil)

	store.routes().ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", response.Code)
	}
	if location := response.Header().Get("Location"); location != "/" {
		t.Fatalf("expected redirect to /, got %q", location)
	}
}

func TestProtectedFormsUseSessionEmailAndCSRF(t *testing.T) {
	const (
		email = "ben@example.com"
		mac   = "82:00:3b:d0:93:12"
	)

	store := testStore(t)
	handler := store.routes()

	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, formRequest(http.MethodPost, "/login", url.Values{
		"email": {email},
	}))
	if loginResponse.Code != http.StatusSeeOther {
		t.Fatalf("expected login redirect, got %d", loginResponse.Code)
	}

	cookie := sessionCookie(t, loginResponse.Result())
	current := storedSession(t, store, cookie.Value)

	addResponse := httptest.NewRecorder()
	addRequest := formRequest(http.MethodPost, "/mac-address", url.Values{
		csrfFormField: {"wrong-token"},
		"macAddress":  {mac},
	})
	addRequest.AddCookie(cookie)
	handler.ServeHTTP(addResponse, addRequest)
	if addResponse.Code != http.StatusForbidden {
		t.Fatalf("expected invalid CSRF to be forbidden, got %d", addResponse.Code)
	}
	if contains(store.Accounts[email], mac) {
		t.Fatal("MAC address was added despite invalid CSRF token")
	}

	addResponse = httptest.NewRecorder()
	addRequest = formRequest(http.MethodPost, "/mac-address", url.Values{
		csrfFormField: {current.CSRFToken},
		"email":       {"attacker@example.com"},
		"macAddress":  {mac},
	})
	addRequest.AddCookie(cookie)
	handler.ServeHTTP(addResponse, addRequest)
	if addResponse.Code != http.StatusSeeOther {
		t.Fatalf("expected add redirect, got %d", addResponse.Code)
	}
	if !contains(store.Accounts[email], mac) {
		t.Fatal("MAC address was not added to session account")
	}
	if _, ok := store.Accounts["attacker@example.com"]; ok {
		t.Fatal("form email created or modified a different account")
	}
}

func TestAddMACNormalizesUserInput(t *testing.T) {
	const (
		email = "ben@example.com"
		want  = "82:00:3b:d0:93:12"
	)

	store := testStore(t)
	handler := store.routes()

	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, formRequest(http.MethodPost, "/login", url.Values{
		"email": {email},
	}))
	cookie := sessionCookie(t, loginResponse.Result())
	current := storedSession(t, store, cookie.Value)

	addResponse := httptest.NewRecorder()
	addRequest := formRequest(http.MethodPost, "/mac-address", url.Values{
		csrfFormField: {current.CSRFToken},
		"macAddress":  {"82003BD09312"},
	})
	addRequest.AddCookie(cookie)
	handler.ServeHTTP(addResponse, addRequest)
	if addResponse.Code != http.StatusSeeOther {
		t.Fatalf("expected add redirect, got %d", addResponse.Code)
	}
	if got := store.Accounts[email]; len(got) != 1 || got[0] != want {
		t.Fatalf("stored MAC addresses = %#v, want []string{%q}", got, want)
	}
}

func TestAccountPageIgnoresEmailQuery(t *testing.T) {
	const email = "ben@example.com"

	restoreScanner := replaceScannerForTest(t, nil)
	defer restoreScanner()

	store := testStore(t)
	handler := store.routes()

	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, formRequest(http.MethodPost, "/login", url.Values{
		"email": {email},
	}))
	cookie := sessionCookie(t, loginResponse.Result())

	accountResponse := httptest.NewRecorder()
	accountRequest := httptest.NewRequest(http.MethodGet, "/account?email=attacker@example.com", nil)
	accountRequest.AddCookie(cookie)
	handler.ServeHTTP(accountResponse, accountRequest)

	if accountResponse.Code != http.StatusOK {
		t.Fatalf("expected account page, got %d", accountResponse.Code)
	}
	if !strings.Contains(accountResponse.Body.String(), "Devices for "+email) {
		t.Fatalf("account page did not render session email: %s", accountResponse.Body.String())
	}
	if strings.Contains(accountResponse.Body.String(), "attacker@example.com") {
		t.Fatal("account page used email query parameter")
	}
}

func TestAccountPageHidesScannedMACsRegisteredToAnyAccount(t *testing.T) {
	const (
		email        = "ben@example.com"
		otherEmail   = "other@example.com"
		currentMAC   = "82:00:3b:d0:93:12"
		otherMAC     = "82:00:3b:d0:93:13"
		availableMAC = "82:00:3b:d0:93:14"
	)

	restoreScanner := replaceScannerForTest(t, []networkDevice{
		{IP: "10.0.0.2", MAC: currentMAC},
		{IP: "10.0.0.3", MAC: otherMAC},
		{IP: "10.0.0.4", MAC: availableMAC},
	})
	defer restoreScanner()

	store := testStore(t)
	store.Accounts[email] = []string{currentMAC}
	store.Accounts[otherEmail] = []string{otherMAC}
	handler := store.routes()

	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, formRequest(http.MethodPost, "/login", url.Values{
		"email": {email},
	}))
	cookie := sessionCookie(t, loginResponse.Result())

	accountResponse := httptest.NewRecorder()
	accountRequest := httptest.NewRequest(http.MethodGet, "/account", nil)
	accountRequest.AddCookie(cookie)
	handler.ServeHTTP(accountResponse, accountRequest)

	if accountResponse.Code != http.StatusOK {
		t.Fatalf("expected account page, got %d", accountResponse.Code)
	}

	body := accountResponse.Body.String()
	if strings.Contains(body, "10.0.0.2") {
		t.Fatal("scan showed a MAC address registered to the current account")
	}
	if strings.Contains(body, otherMAC) || strings.Contains(body, "10.0.0.3") {
		t.Fatal("scan showed a MAC address registered to another account")
	}
	if !strings.Contains(body, availableMAC) || !strings.Contains(body, "10.0.0.4") {
		t.Fatal("scan did not show an unregistered MAC address")
	}
}

func TestOAuthCallbackErrorPageUsesRootRelativeStylesheet(t *testing.T) {
	store := testStore(t)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/login/complete?error=access_denied", nil)
	store.routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected error page, got %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `href="/index.css"`) {
		t.Fatalf("expected root-relative stylesheet link, got: %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), `href="index.css"`) {
		t.Fatal("error page used a path-relative stylesheet link")
	}
}

func TestFaviconRequestDoesNot404(t *testing.T) {
	store := testStore(t)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	store.routes().ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("expected no-content favicon response, got %d", response.Code)
	}
}

func TestOAuthCallbackSavesTokenForMatchingAuthenticatedEmail(t *testing.T) {
	const email = "claimed@example.com"

	store := testStore(t)
	state, err := store.newOAuthState(email, "http://localhost:8080/login/complete")
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			if err := json.NewEncoder(w).Encode(map[string]string{
				"access_token": "claimed-token",
				"token_type":   "bearer",
			}); err != nil {
				t.Fatal(err)
			}
		case "/api/v1/profiles/me":
			if r.Header.Get("Authorization") != "Bearer claimed-token" {
				t.Fatalf("unexpected authorization header: %s", r.Header.Get("Authorization"))
			}
			if err := json.NewEncoder(w).Encode(map[string]string{
				"id":    "123",
				"email": "CLAIMED@example.com",
			}); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	t.Setenv("VALET_OAUTH_CLIENT_ID", "client-id")
	t.Setenv("VALET_OAUTH_CLIENT_SECRET", "client-secret")
	t.Setenv("VALET_OAUTH_TOKEN_URL", server.URL+"/oauth/token")
	t.Setenv("VALET_RC_BASE_URL", server.URL)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/login/complete?state="+url.QueryEscape(state)+"&code=auth-code", nil)
	store.routes().ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("expected account redirect, got %d: %s", response.Code, response.Body.String())
	}
	token, ok := store.oauthToken(email)
	if !ok || token.AccessToken != "claimed-token" {
		t.Fatalf("expected saved OAuth token, got %#v", token)
	}
	if got := store.RCProfileIDs[email]; got != "123" {
		t.Fatalf("expected cached RC profile id 123, got %q", got)
	}
}

func TestOAuthCallbackRejectsMismatchedAuthenticatedEmail(t *testing.T) {
	const (
		claimedEmail       = "claimed@example.com"
		authenticatedEmail = "actual@example.com"
	)

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
			if r.Header.Get("Authorization") != "Bearer actual-token" {
				t.Fatalf("unexpected authorization header: %s", r.Header.Get("Authorization"))
			}
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

	t.Setenv("VALET_OAUTH_CLIENT_ID", "client-id")
	t.Setenv("VALET_OAUTH_CLIENT_SECRET", "client-secret")
	t.Setenv("VALET_OAUTH_TOKEN_URL", server.URL+"/oauth/token")
	t.Setenv("VALET_RC_BASE_URL", server.URL)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/login/complete?state="+url.QueryEscape(state)+"&code=auth-code", nil)
	store.routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected error page, got %d", response.Code)
	}
	if store.hasOAuthToken(claimedEmail) {
		t.Fatal("mismatched OAuth account was saved as authorized")
	}
	if !strings.Contains(response.Body.String(), "OAuth account "+authenticatedEmail+" does not match "+claimedEmail) {
		t.Fatalf("expected email mismatch error, got: %s", response.Body.String())
	}
}

func testStore(t *testing.T) *accountStore {
	t.Helper()

	store, err := openStore(filepath.Join(t.TempDir(), "accounts.json"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return store
}

func formRequest(method, target string, form url.Values) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}

func sessionCookie(t *testing.T, response *http.Response) *http.Cookie {
	t.Helper()

	for _, cookie := range response.Cookies() {
		if cookie.Name == sessionCookieName {
			return cookie
		}
	}
	t.Fatal("response did not set session cookie")
	return nil
}

func storedSession(t *testing.T, store *accountStore, sessionID string) session {
	t.Helper()

	store.mu.Lock()
	defer store.mu.Unlock()
	current, ok := store.sessions[sessionID]
	if !ok {
		t.Fatal("session cookie was not stored")
	}
	return current
}
