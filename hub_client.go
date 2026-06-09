package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type hubVisitClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

type rcProfile struct {
	ID    string
	Email string
}

func newHubVisitClient(cfg hubMonitorConfig) *hubVisitClient {
	return &hubVisitClient{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *hubVisitClient) withToken(token string) *hubVisitClient {
	copy := *c
	copy.token = token
	return &copy
}

func (c *hubVisitClient) findRCProfileID(ctx context.Context, email string) (string, error) {
	u, err := url.Parse(c.baseURL + "/api/v1/profiles")
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("query", email)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("profile lookup failed: %s: %s", resp.Status, readResponseSnippet(resp.Body))
	}

	var profiles []struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&profiles); err != nil {
		return "", err
	}
	if len(profiles) == 0 {
		return "", fmt.Errorf("no RC profile found for %s", email)
	}
	return rawRCProfileID(profiles[0].ID)
}

func (c *hubVisitClient) authenticatedRCProfile(ctx context.Context) (rcProfile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/profiles/me", nil)
	if err != nil {
		return rcProfile{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return rcProfile{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return rcProfile{}, fmt.Errorf("authenticated profile lookup failed: %s: %s", resp.Status, readResponseSnippet(resp.Body))
	}

	var response struct {
		ID           json.RawMessage `json:"id"`
		Email        string          `json:"email"`
		EmailAddress string          `json:"email_address"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return rcProfile{}, err
	}

	email := response.Email
	if email == "" {
		email = response.EmailAddress
	}
	if email == "" {
		return rcProfile{}, errors.New("authenticated profile response missing email")
	}

	profile := rcProfile{Email: email}
	if len(response.ID) > 0 && string(response.ID) != "null" {
		id, err := rawRCProfileID(response.ID)
		if err != nil {
			return rcProfile{}, err
		}
		profile.ID = id
	}
	return profile, nil
}

func (c *hubVisitClient) markHubVisit(ctx context.Context, rcProfileID, date string) error {
	u := c.baseURL + "/api/v1/hub_visits/" + url.PathEscape(rcProfileID) + "/" + url.PathEscape(date)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "V.A.L.E.T.")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "*/*")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("hub visit update failed: %s: %s", resp.Status, readResponseSnippet(resp.Body))
	}
	return nil
}

func rawRCProfileID(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("profile response missing id")
	}

	var idString string
	if err := json.Unmarshal(raw, &idString); err == nil {
		if idString == "" {
			return "", errors.New("profile response has empty id")
		}
		return idString, nil
	}

	id := strings.TrimSpace(string(raw))
	if id == "" || id == "null" {
		return "", errors.New("profile response has empty id")
	}
	return id, nil
}

func readResponseSnippet(body io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(body, 4096))
	if err != nil {
		return err.Error()
	}
	return strings.TrimSpace(string(data))
}
