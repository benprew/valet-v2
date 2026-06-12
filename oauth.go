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

// oauthGrant is the result of an OAuth code exchange or refresh. The
// access token is used immediately and never persisted; only the refresh
// token is stored.
type oauthGrant struct {
	AccessToken  string
	RefreshToken string
	Scope        string
	ExpiresAt    string
}

func (g oauthGrant) storedToken() token {
	return token{
		Type:         tokenTypeOAuth,
		RefreshToken: g.RefreshToken,
		Scope:        g.Scope,
		ExpiresAt:    g.ExpiresAt,
	}
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

func (c oauthConfig) authorizeURL(state string, emails ...string) (string, error) {
	u, err := url.Parse(c.AuthorizeURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("client_id", c.ClientID)
	q.Set("redirect_uri", c.RedirectURL)
	q.Set("response_type", "code")
	q.Set("state", state)
	if len(emails) > 0 && emails[0] != "" {
		q.Set("login_hint", emails[0])
		q.Set("prompt", "login")
	}
	if c.Scope != "" {
		q.Set("scope", c.Scope)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c oauthConfig) exchangeCode(ctx context.Context, code string) (oauthGrant, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	form.Set("redirect_uri", c.RedirectURL)

	return c.requestToken(ctx, form, oauthGrant{}, "OAuth token exchange")
}

func (c oauthConfig) refreshToken(ctx context.Context, current token) (oauthGrant, error) {
	if err := c.validateRefresh(); err != nil {
		return oauthGrant{}, err
	}
	if current.RefreshToken == "" {
		return oauthGrant{}, errors.New("OAuth token has no refresh token")
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", current.RefreshToken)
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)

	fallback := oauthGrant{
		RefreshToken: current.RefreshToken,
		Scope:        current.Scope,
		ExpiresAt:    current.ExpiresAt,
	}
	return c.requestToken(ctx, form, fallback, "OAuth token refresh")
}

func (c oauthConfig) requestToken(ctx context.Context, form url.Values, fallback oauthGrant, action string) (oauthGrant, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthGrant{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := oauthHTTPClient().Do(req)
	if err != nil {
		return oauthGrant{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return oauthGrant{}, fmt.Errorf("%s failed: %s: %s", action, resp.Status, readResponseSnippet(resp.Body))
	}

	return decodeOAuthTokenResponse(resp.Body, fallback)
}

func decodeOAuthTokenResponse(body io.Reader, fallback oauthGrant) (oauthGrant, error) {
	var response struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Scope        string `json:"scope"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		return oauthGrant{}, err
	}
	if response.AccessToken == "" {
		return oauthGrant{}, errors.New("OAuth token response returned no access token")
	}

	grant := oauthGrant{
		AccessToken:  response.AccessToken,
		RefreshToken: response.RefreshToken,
		Scope:        response.Scope,
	}
	if grant.RefreshToken == "" {
		grant.RefreshToken = fallback.RefreshToken
	}
	if grant.Scope == "" {
		grant.Scope = fallback.Scope
	}
	if response.ExpiresIn > 0 {
		grant.ExpiresAt = time.Now().Add(time.Duration(response.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	} else {
		grant.ExpiresAt = fallback.ExpiresAt
	}
	return grant, nil
}

func oauthHTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}

// bearerToken returns a token for an Authorization header. Personal access
// tokens are used directly; OAuth refresh tokens are exchanged for a fresh
// access token, and the (possibly rotated) refresh token is saved.
func (s *accountStore) bearerToken(ctx context.Context, email string) (string, error) {
	current, ok, err := s.token(email)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("account has not authorized Hub access")
	}
	if current.Type == tokenTypePAT {
		return current.RefreshToken, nil
	}

	grant, err := oauthConfigFromRequest(nil).refreshToken(ctx, current)
	if err != nil {
		return "", err
	}
	if err := s.saveToken(email, grant.storedToken()); err != nil {
		return "", fmt.Errorf("save refreshed OAuth token: %w", err)
	}
	return grant.AccessToken, nil
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
