package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"

	_ "modernc.org/sqlite"
)

type accountStore struct {
	path         string
	db           *sql.DB
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
	return filepath.Join("data", "accounts.db")
}

func openStore(path string) (*accountStore, error) {
	store := newAccountStore(path)
	dbExisted := true
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		dbExisted = false
	}

	if err := store.openDBLocked(); err != nil {
		return nil, err
	}
	if err := store.loadFromDB(); err != nil {
		return nil, err
	}

	if !dbExisted && store.empty() {
		legacy, err := loadLegacyJSONStore(legacyJSONPath(path))
		if err != nil {
			return nil, err
		}
		if legacy != nil {
			store.Accounts = legacy.Accounts
			store.HubVisits = legacy.HubVisits
			store.OAuthTokens = legacy.OAuthTokens
			store.RCProfileIDs = legacy.RCProfileIDs
			if err := store.saveLocked(); err != nil {
				return nil, err
			}
		}
	}

	return store, nil
}

func newAccountStore(path string) *accountStore {
	return &accountStore{
		path:         path,
		Accounts:     map[string][]string{},
		HubVisits:    map[string]string{},
		OAuthTokens:  map[string]oauthToken{},
		RCProfileIDs: map[string]string{},
		sessions:     map[string]session{},
		oauthStates:  map[string]oauthState{},
	}
}

func (s *accountStore) openDBLocked() error {
	if s.db != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`
		PRAGMA foreign_keys = ON;
		CREATE TABLE IF NOT EXISTS accounts (
			email TEXT PRIMARY KEY
		);
		CREATE TABLE IF NOT EXISTS account_macs (
			email TEXT NOT NULL,
			mac_address TEXT NOT NULL,
			PRIMARY KEY (email, mac_address),
			FOREIGN KEY (email) REFERENCES accounts(email) ON DELETE CASCADE
		);
		CREATE TABLE IF NOT EXISTS hub_visits (
			email TEXT PRIMARY KEY,
			date TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS oauth_tokens (
			email TEXT PRIMARY KEY,
			access_token TEXT NOT NULL,
			token_type TEXT NOT NULL DEFAULT '',
			refresh_token TEXT NOT NULL DEFAULT '',
			scope TEXT NOT NULL DEFAULT '',
			expires_at TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS rc_profile_ids (
			email TEXT PRIMARY KEY,
			rc_profile_id TEXT NOT NULL
		);
	`); err != nil {
		db.Close()
		return err
	}
	s.db = db
	return nil
}

func (s *accountStore) loadFromDB() error {
	if _, err := s.Accts(); err != nil {
		return err
	}
	if err := s.loadHubVisits(); err != nil {
		return err
	}
	if err := s.loadOAuthTokens(); err != nil {
		return err
	}
	return s.loadRCProfileIDs()
}

func (s *accountStore) Accts() (map[string][]string, error) {
	rows, err := s.db.Query("SELECT email FROM accounts ORDER BY email")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, err
		}
		s.Accounts[email] = []string{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.db.Query("SELECT email, mac_address FROM account_macs ORDER BY email, mac_address")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var email, mac string
		if err := rows.Scan(&email, &mac); err != nil {
			return nil, err
		}
		s.Accounts[email] = append(s.Accounts[email], mac)
	}
	return s.Accounts, nil
}

func (s *accountStore) loadHubVisits() error {
	rows, err := s.db.Query("SELECT email, date FROM hub_visits")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var email, date string
		if err := rows.Scan(&email, &date); err != nil {
			return err
		}
		s.HubVisits[email] = date
	}
	return rows.Err()
}

func (s *accountStore) loadOAuthTokens() error {
	rows, err := s.db.Query(`
		SELECT email, access_token, token_type, refresh_token, scope, expires_at
		FROM oauth_tokens
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var email string
		var token oauthToken
		if err := rows.Scan(&email, &token.AccessToken, &token.TokenType, &token.RefreshToken, &token.Scope, &token.ExpiresAt); err != nil {
			return err
		}
		s.OAuthTokens[email] = token
	}
	return rows.Err()
}

func (s *accountStore) loadRCProfileIDs() error {
	rows, err := s.db.Query("SELECT email, rc_profile_id FROM rc_profile_ids")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var email, rcProfileID string
		if err := rows.Scan(&email, &rcProfileID); err != nil {
			return err
		}
		s.RCProfileIDs[email] = rcProfileID
	}
	return rows.Err()
}

func (s *accountStore) saveLocked() error {
	if err := s.openDBLocked(); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, table := range []string{"account_macs", "accounts", "hub_visits", "oauth_tokens", "rc_profile_ids"} {
		if _, err := tx.Exec("DELETE FROM " + table); err != nil {
			return err
		}
	}

	emails := s.storeEmailsLocked()
	for _, email := range emails {
		if _, err := tx.Exec("INSERT INTO accounts(email) VALUES (?)", email); err != nil {
			return err
		}
	}

	for email, macAddresses := range s.Accounts {
		for _, mac := range macAddresses {
			if _, err := tx.Exec("INSERT OR IGNORE INTO account_macs(email, mac_address) VALUES (?, ?)", email, mac); err != nil {
				return err
			}
		}
	}

	for email, date := range s.HubVisits {
		if _, err := tx.Exec("INSERT INTO hub_visits(email, date) VALUES (?, ?)", email, date); err != nil {
			return err
		}
	}

	for email, token := range s.OAuthTokens {
		if _, err := tx.Exec(`
			INSERT INTO oauth_tokens(email, access_token, token_type, refresh_token, scope, expires_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, email, token.AccessToken, token.TokenType, token.RefreshToken, token.Scope, token.ExpiresAt); err != nil {
			return err
		}
	}

	for email, rcProfileID := range s.RCProfileIDs {
		if _, err := tx.Exec("INSERT INTO rc_profile_ids(email, rc_profile_id) VALUES (?, ?)", email, rcProfileID); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *accountStore) storeEmailsLocked() []string {
	emails := map[string]struct{}{}
	for email := range s.Accounts {
		emails[email] = struct{}{}
	}
	for email := range s.HubVisits {
		emails[email] = struct{}{}
	}
	for email := range s.OAuthTokens {
		emails[email] = struct{}{}
	}
	for email := range s.RCProfileIDs {
		emails[email] = struct{}{}
	}

	list := make([]string, 0, len(emails))
	for email := range emails {
		list = append(list, email)
	}
	sort.Strings(list)
	return list
}

func (s *accountStore) empty() bool {
	return len(s.Accounts) == 0 && len(s.HubVisits) == 0 && len(s.OAuthTokens) == 0 && len(s.RCProfileIDs) == 0
}

func legacyJSONPath(dbPath string) string {
	extension := filepath.Ext(dbPath)
	if extension == "" {
		return dbPath + ".json"
	}
	return dbPath[:len(dbPath)-len(extension)] + ".json"
}

func loadLegacyJSONStore(path string) (*accountStore, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	store := newAccountStore("")
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
	return store, nil
}
