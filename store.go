package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type accountStore struct {
	path         string
	mu           sync.Mutex
	Accounts     map[string][]string   `json:"accounts"`
	HubVisits    map[string]string     `json:"hub_visits,omitempty"`
	OAuthTokens  map[string]oauthToken `json:"oauth_tokens,omitempty"`
	RCProfileIDs map[string]string     `json:"rc_profile_ids,omitempty"`
	sessions     map[string]session
	oauthStates  map[string]oauthState
}

func dataPath() string {
	if path := os.Getenv("VALET_DATA"); path != "" {
		return path
	}
	return filepath.Join("data", "accounts.json")
}

func openStore(path string) (*accountStore, error) {
	store := &accountStore{
		path:         path,
		Accounts:     map[string][]string{},
		HubVisits:    map[string]string{},
		OAuthTokens:  map[string]oauthToken{},
		RCProfileIDs: map[string]string{},
		sessions:     map[string]session{},
		oauthStates:  map[string]oauthState{},
	}

	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	if err := json.NewDecoder(file).Decode(store); err != nil {
		return nil, err
	}
	if store.Accounts == nil {
		store.Accounts = map[string][]string{}
	}
	if store.HubVisits == nil {
		store.HubVisits = map[string]string{}
	}
	if store.OAuthTokens == nil {
		store.OAuthTokens = map[string]oauthToken{}
	}
	if store.RCProfileIDs == nil {
		store.RCProfileIDs = map[string]string{}
	}
	store.sessions = map[string]session{}
	store.oauthStates = map[string]oauthState{}
	return store, nil
}

func (s *accountStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	tempFile, err := os.CreateTemp(filepath.Dir(s.path), ".accounts-*.json")
	if err != nil {
		return err
	}
	tempName := tempFile.Name()

	encoder := json.NewEncoder(tempFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(s); err != nil {
		tempFile.Close()
		os.Remove(tempName)
		return err
	}
	if err := tempFile.Close(); err != nil {
		os.Remove(tempName)
		return err
	}
	return os.Rename(tempName, s.path)
}
