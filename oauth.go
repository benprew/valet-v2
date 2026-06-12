package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const oauthRefreshSkew = time.Minute

type oauthConfig struct {
	ClientID     string
	ClientSecret string
	AuthorizeURL string
	TokenURL     string
	RedirectURL  string
	Scope        string
}

type oauthState struct {
	Email       string
	RedirectURL string
	CreatedAt   time.Time
}

type oauthToken struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
}

func oauthConfigFromRequest(r *http.Request) oauthConfig {
	redirectURL := conf.OAuthRedirectURL
	if redirectURL == "" && r != nil {
		redirectURL = inferOAuthRedirectURL(r)
	}

	cfg := oauthConfig{
		ClientID:     conf.OAuthClientID,
		ClientSecret: conf.OAuthClientSecret,
		AuthorizeURL: strings.TrimSpace(conf.OAuthAuthorizeURL),
		TokenURL:     strings.TrimSpace(conf.OAuthTokenURL),
		RedirectURL:  redirectURL,
		Scope:        strings.TrimSpace(conf.OAuthScope),
	}
	if cfg.AuthorizeURL == "" {
		cfg.AuthorizeURL = rcBaseURL() + "/oauth/authorize"
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = rcBaseURL() + "/oauth/token"
	}
	return cfg
}

func (c oauthConfig) configured() bool {
	return c.ClientID != "" && c.ClientSecret != ""
}

func (c oauthConfig) validate() error {
	if c.ClientID == "" {
		return errors.New("-oauth-client-id is not set")
	}
	if c.ClientSecret == "" {
		return errors.New("-oauth-client-secret is not set")
	}
	if c.RedirectURL == "" {
		return errors.New("OAuth redirect URL could not be inferred; set -oauth-redirect-url")
	}
	if c.AuthorizeURL == "" {
		return errors.New("OAuth authorize URL is not set")
	}
	if c.TokenURL == "" {
		return errors.New("OAuth token URL is not set")
	}
	return nil
}

func (c oauthConfig) validateRefresh() error {
	if c.ClientID == "" {
		return errors.New("-oauth-client-id is not set")
	}
	if c.ClientSecret == "" {
		return errors.New("-oauth-client-secret is not set")
	}
	if c.TokenURL == "" {
		return errors.New("OAuth token URL is not set")
	}
	return nil
}

func (c oauthConfig) authorizeURL(state string) (string, error) {
	u, err := url.Parse(c.AuthorizeURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("client_id", c.ClientID)
	q.Set("redirect_uri", c.RedirectURL)
	q.Set("response_type", "code")
	q.Set("state", state)
	if c.Scope != "" {
		q.Set("scope", c.Scope)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c oauthConfig) exchangeCode(ctx context.Context, code string) (oauthToken, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	form.Set("redirect_uri", c.RedirectURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := oauthHTTPClient().Do(req)
	if err != nil {
		return oauthToken{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return oauthToken{}, fmt.Errorf("OAuth token exchange failed: %s: %s", resp.Status, readResponseSnippet(resp.Body))
	}

	return decodeOAuthTokenResponse(resp.Body, oauthToken{})
}

func (c oauthConfig) refreshToken(ctx context.Context, current oauthToken) (oauthToken, error) {
	if err := c.validateRefresh(); err != nil {
		return oauthToken{}, err
	}
	if current.RefreshToken == "" {
		return oauthToken{}, errors.New("OAuth token has no refresh token")
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", current.RefreshToken)
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := oauthHTTPClient().Do(req)
	if err != nil {
		return oauthToken{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return oauthToken{}, fmt.Errorf("OAuth token refresh failed: %s: %s", resp.Status, readResponseSnippet(resp.Body))
	}

	return decodeOAuthTokenResponse(resp.Body, current)
}

func decodeOAuthTokenResponse(body io.Reader, fallback oauthToken) (oauthToken, error) {
	var response struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		RefreshToken string `json:"refresh_token"`
		Scope        string `json:"scope"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		return oauthToken{}, err
	}
	if response.AccessToken == "" {
		return oauthToken{}, errors.New("OAuth token response returned no access token")
	}

	token := oauthToken{
		AccessToken:  response.AccessToken,
		TokenType:    response.TokenType,
		RefreshToken: response.RefreshToken,
		Scope:        response.Scope,
	}
	if token.TokenType == "" {
		token.TokenType = fallback.TokenType
	}
	if token.RefreshToken == "" {
		token.RefreshToken = fallback.RefreshToken
	}
	if token.Scope == "" {
		token.Scope = fallback.Scope
	}
	if response.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(response.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	} else {
		token.ExpiresAt = fallback.ExpiresAt
	}
	return token, nil
}

func oauthHTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}

func (t oauthToken) canAuthorize() bool {
	return t.AccessToken != "" || t.RefreshToken != ""
}

func (t oauthToken) needsRefresh(now time.Time) bool {
	if t.AccessToken == "" {
		return t.RefreshToken != ""
	}
	if t.ExpiresAt == "" {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339, t.ExpiresAt)
	if err != nil {
		return false
	}
	return !expiresAt.After(now.Add(oauthRefreshSkew))
}

func (s *accountStore) newOAuthState(email, redirectURL string) (string, error) {
	state, err := randomState()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.oauthStates[state] = oauthState{
		Email:       email,
		RedirectURL: redirectURL,
		CreatedAt:   time.Now(),
	}
	return state, nil
}

func (s *accountStore) consumeOAuthState(state string) (oauthState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	value, ok := s.oauthStates[state]
	if ok {
		delete(s.oauthStates, state)
	}
	if !ok || time.Since(value.CreatedAt) > 10*time.Minute {
		return oauthState{}, false
	}
	return value, true
}

func (s *accountStore) saveOAuthToken(email string, token oauthToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.OAuthTokens[email] = token
	return s.saveLocked()
}

func (s *accountStore) oauthToken(email string) (oauthToken, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.OAuthTokens[email]
	return token, ok && token.canAuthorize()
}

func (s *accountStore) hasOAuthToken(email string) bool {
	_, ok := s.oauthToken(email)
	return ok
}

func (s *accountStore) usableOAuthToken(ctx context.Context, email string) (oauthToken, error) {
	token, ok := s.oauthToken(email)
	if !ok {
		return oauthToken{}, errors.New("account has not authorized Hub access with OAuth")
	}
	if !token.needsRefresh(time.Now()) {
		return token, nil
	}
	if token.RefreshToken == "" {
		return oauthToken{}, errors.New("OAuth access token is expired and no refresh token is available")
	}

	refreshed, err := oauthConfigFromRequest(nil).refreshToken(ctx, token)
	if err != nil {
		return oauthToken{}, err
	}
	if err := s.saveOAuthToken(email, refreshed); err != nil {
		return oauthToken{}, fmt.Errorf("save refreshed OAuth token: %w", err)
	}
	return refreshed, nil
}

func inferOAuthRedirectURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := r.Host
	if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
		host = forwardedHost
	}
	return scheme + "://" + host + "/login/complete"
}

func randomState() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}
