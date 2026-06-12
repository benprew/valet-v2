package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"sort"
	"time"
)

//go:embed index.html
var pageHTML string

//go:embed index.css
var indexCSS string

var pageTmpl = template.Must(template.New("index.html").Parse(pageHTML))

var (
	errCreateOAuthState           = errors.New("create OAuth state")
	errBuildOAuthAuthorizationURL = errors.New("build OAuth authorization URL")
)

type pageData struct {
	Email           string
	MacAddresses    []string
	Devices         []networkDevice
	HubAuthorized   bool
	LastHubVisit    string
	OAuthConfigured bool
	CSRFToken       string
	Error           string
}

func (s *accountStore) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /index.css", serveCSS)
	mux.HandleFunc("GET /favicon.ico", serveFavicon)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("GET /account", s.handleAccount)
	mux.HandleFunc("POST /oauth/start", s.handleOAuthStart)
	mux.HandleFunc("GET /login/complete", s.handleOAuthCallback)
	mux.HandleFunc("GET /oauth/callback", s.handleOAuthCallback)
	mux.HandleFunc("POST /mac-address", s.handleAddMAC)
	mux.HandleFunc("POST /mac-address/delete", s.handleDeleteMAC)
	return mux
}

func (s *accountStore) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if _, ok := s.currentSession(r); ok {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	renderPage(w, pageData{})
}

func (s *accountStore) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		renderPage(w, pageData{Error: "Could not read login form."})
		return
	}

	email, err := normalizeEmail(r.FormValue("email"))
	if err != nil {
		renderPage(w, pageData{Error: err.Error()})
		return
	}

	s.mu.Lock()
	if _, ok := s.Accounts[email]; !ok {
		s.Accounts[email] = []string{}
		if err := s.saveLocked(); err != nil {
			s.mu.Unlock()
			renderPage(w, pageData{Error: "Could not save account."})
			return
		}
	}
	s.mu.Unlock()

	var authorizeURL string
	cfg := oauthConfigFromRequest(r)
	if cfg.configured() && !s.hasOAuthToken(email) {
		authorizeURL, err = s.oauthAuthorizationURL(email, cfg)
		if err != nil {
			log.Printf("start OAuth authorization for %s after login: %v", email, err)
		}
	}

	if err := s.startSession(w, r, email); err != nil {
		renderPage(w, pageData{Error: "Could not start session."})
		return
	}

	if authorizeURL != "" {
		http.Redirect(w, r, authorizeURL, http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func (s *accountStore) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "could not read logout form", http.StatusBadRequest)
		return
	}
	if !s.validateCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}

	s.endSession(w, r)
	scheduleKioskResetAfterResponse(r, "logout")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *accountStore) handleAccount(w http.ResponseWriter, r *http.Request) {
	current, ok := s.currentSession(r)
	if !ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	data := s.pageDataForSession(r.Context(), current)
	renderPage(w, data)
}

func (s *accountStore) handleAddMAC(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		renderPage(w, pageData{Error: "Could not read MAC address form."})
		return
	}
	current, ok := s.validFormSession(w, r)
	if !ok {
		return
	}
	mac, err := normalizeMAC(r.FormValue("macAddress"))
	if err != nil {
		data := s.pageDataForSession(r.Context(), current)
		data.Error = err.Error()
		renderPage(w, data)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.Accounts[current.Email]
	if !contains(list, mac) {
		list = append(list, mac)
		sort.Strings(list)
		s.Accounts[current.Email] = list
		if err := s.saveLocked(); err != nil {
			data := s.pageDataForSession(r.Context(), current)
			data.Error = "Could not save MAC address."
			renderPage(w, data)
			return
		}
	}

	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func (s *accountStore) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		renderPage(w, pageData{Error: "Could not read authorization form."})
		return
	}
	current, ok := s.validFormSession(w, r)
	if !ok {
		return
	}

	cfg := oauthConfigFromRequest(r)
	authorizeURL, err := s.oauthAuthorizationURL(current.Email, cfg)
	if err != nil {
		data := s.pageDataForSession(r.Context(), current)
		data.Error = oauthAuthorizationError(err)
		renderPage(w, data)
		return
	}
	http.Redirect(w, r, authorizeURL, http.StatusSeeOther)
}

func (s *accountStore) oauthAuthorizationURL(email string, cfg oauthConfig) (string, error) {
	if err := cfg.validate(); err != nil {
		return "", err
	}

	state, err := s.newOAuthState(email, cfg.RedirectURL)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errCreateOAuthState, err)
	}

	authorizeURL, err := cfg.authorizeURL(state, email)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errBuildOAuthAuthorizationURL, err)
	}
	return authorizeURL, nil
}

func oauthAuthorizationError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errCreateOAuthState):
		return "Could not start OAuth authorization."
	case errors.Is(err, errBuildOAuthAuthorizationURL):
		return "Could not build OAuth authorization URL."
	default:
		return err.Error()
	}
}

func (s *accountStore) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if oauthError := r.URL.Query().Get("error"); oauthError != "" {
		scheduleKioskResetAfterResponse(r, "OAuth authorization error")
		renderPage(w, pageData{Error: "OAuth authorization failed: " + oauthError})
		return
	}

	stateValue := r.URL.Query().Get("state")
	state, ok := s.consumeOAuthState(stateValue)
	if !ok {
		scheduleKioskResetAfterResponse(r, "invalid OAuth state")
		renderPage(w, pageData{Error: "OAuth authorization state is invalid or expired."})
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		data := s.pageData(r.Context(), state.Email)
		data.Error = "OAuth authorization did not return a code."
		renderPage(w, data)
		return
	}

	cfg := oauthConfigFromRequest(r)
	cfg.RedirectURL = state.RedirectURL
	if err := cfg.validate(); err != nil {
		data := s.pageData(r.Context(), state.Email)
		data.Error = err.Error()
		renderPage(w, data)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	token, err := cfg.exchangeCode(ctx, code)
	if err != nil {
		data := s.pageData(r.Context(), state.Email)
		data.Error = err.Error()
		renderPage(w, data)
		return
	}

	profile, err := newHubVisitClient(currentHubMonitorConfig()).withToken(token.AccessToken).authenticatedRCProfile(ctx)
	if err != nil {
		data := s.pageData(r.Context(), state.Email)
		data.Error = "Could not verify OAuth account: " + err.Error()
		renderPage(w, data)
		return
	}
	profileEmail, err := normalizeEmail(profile.Email)
	if err != nil {
		data := s.pageData(r.Context(), state.Email)
		data.Error = "Could not verify OAuth account email."
		renderPage(w, data)
		return
	}
	if profileEmail != state.Email {
		data := s.pageData(r.Context(), state.Email)
		data.Error = fmt.Sprintf("OAuth account %s does not match %s.", profileEmail, state.Email)
		scheduleKioskResetAfterResponse(r, "OAuth account mismatch")
		renderPage(w, data)
		return
	}

	if err := s.saveOAuthToken(state.Email, token); err != nil {
		data := s.pageData(r.Context(), state.Email)
		data.Error = "Could not save OAuth token."
		renderPage(w, data)
		return
	}

	if profile.ID != "" {
		if err := s.cacheRCProfileID(state.Email, profile.ID); err != nil {
			log.Printf("cache OAuth RC profile id for %s: %v", state.Email, err)
		}
	}

	if err := s.startSession(w, r, state.Email); err != nil {
		data := s.pageData(r.Context(), state.Email)
		data.Error = "Could not start session."
		renderPage(w, data)
		return
	}

	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func (s *accountStore) handleDeleteMAC(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		renderPage(w, pageData{Error: "Could not read remove form."})
		return
	}
	current, ok := s.validFormSession(w, r)
	if !ok {
		return
	}
	mac, err := normalizeMAC(r.FormValue("macAddress"))
	if err != nil {
		data := s.pageDataForSession(r.Context(), current)
		data.Error = err.Error()
		renderPage(w, data)
		return
	}

	s.mu.Lock()
	list := s.Accounts[current.Email]
	next := list[:0]
	for _, existing := range list {
		if existing != mac {
			next = append(next, existing)
		}
	}
	s.Accounts[current.Email] = next
	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		data := s.pageDataForSession(r.Context(), current)
		data.Error = "Could not save account."
		renderPage(w, data)
		return
	}
	s.mu.Unlock()

	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func (s *accountStore) pageData(ctx context.Context, email string) pageData {
	s.mu.Lock()
	macAddresses := cloneStrings(s.Accounts[email])
	lastHubVisit := s.HubVisits[email]
	registeredMACs := s.registeredMACSetLocked()
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	devices, err := cachedNetworkDevicesFunc(ctx)
	if err != nil {
		return pageData{
			Email:           email,
			MacAddresses:    macAddresses,
			HubAuthorized:   s.hasOAuthToken(email),
			LastHubVisit:    lastHubVisit,
			OAuthConfigured: oauthConfigFromRequest(nil).configured(),
			Error:           "Network scan failed: " + err.Error(),
		}
	}
	devices = filterRegisteredDevices(devices, registeredMACs)

	return pageData{
		Email:           email,
		MacAddresses:    macAddresses,
		Devices:         devices,
		HubAuthorized:   s.hasOAuthToken(email),
		LastHubVisit:    lastHubVisit,
		OAuthConfigured: oauthConfigFromRequest(nil).configured(),
	}
}

func (s *accountStore) pageDataForSession(ctx context.Context, current session) pageData {
	data := s.pageData(ctx, current.Email)
	data.CSRFToken = current.CSRFToken
	return data
}

func (s *accountStore) registeredMACSetLocked() map[string]struct{} {
	registered := map[string]struct{}{}
	for _, macAddresses := range s.Accounts {
		for _, mac := range macAddresses {
			registered[mac] = struct{}{}
		}
	}
	return registered
}

func filterRegisteredDevices(devices []networkDevice, registeredMACs map[string]struct{}) []networkDevice {
	filtered := devices[:0]
	for _, device := range devices {
		if _, registered := registeredMACs[device.MAC]; registered {
			continue
		}
		filtered = append(filtered, device)
	}
	return filtered
}

func (s *accountStore) validFormSession(w http.ResponseWriter, r *http.Request) (session, bool) {
	current, ok := s.currentSession(r)
	if !ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return session{}, false
	}
	if r.FormValue(csrfFormField) != current.CSRFToken {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return session{}, false
	}
	return current, true
}

func renderPage(w http.ResponseWriter, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTmpl.Execute(w, data); err != nil {
		log.Printf("render page: %v", err)
	}
}

func serveCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	if _, err := io.WriteString(w, indexCSS); err != nil {
		log.Printf("serve css: %v", err)
	}
}

func serveFavicon(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}
