package main

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

const (
	tokenTypeOAuth = "oauth"
	tokenTypePAT   = "pat"
)

// token is a long-lived credential for an account. For OAuth tokens
// RefreshToken is exchanged for a short-lived access token on demand;
// for personal access tokens it holds the token itself.
type token struct {
	Type         string
	RefreshToken string
	Scope        string
	ExpiresAt    string
}

func (t token) canAuthorize() bool {
	return t.RefreshToken != ""
}

// accountStore persists accounts, MAC addresses, and tokens in SQLite,
// querying the database on each use. Hub visits, sessions, and OAuth
// states live only in memory and reset on restart.
type accountStore struct {
	db *sql.DB

	mu          sync.Mutex
	hubVisits   map[string]string
	sessions    map[string]session
	oauthStates map[string]oauthState
}

const storeSchema = `
CREATE TABLE IF NOT EXISTS accounts (
	id INTEGER PRIMARY KEY,
	email TEXT NOT NULL UNIQUE,
	rc_profile_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS macs (
	id INTEGER PRIMARY KEY,
	account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	mac_address TEXT NOT NULL,
	UNIQUE (account_id, mac_address)
);
CREATE TABLE IF NOT EXISTS tokens (
	id INTEGER PRIMARY KEY,
	account_id INTEGER NOT NULL UNIQUE REFERENCES accounts(id) ON DELETE CASCADE,
	token_type TEXT NOT NULL,
	refresh_token TEXT NOT NULL,
	scope TEXT NOT NULL DEFAULT '',
	expires_at TEXT NOT NULL DEFAULT ''
);
`

func openStore(path string) (*accountStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	legacy, err := isLegacySchema(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	if legacy {
		db.Close()
		return nil, fmt.Errorf("%s uses the old database format; migrate it with: go run ./scripts/migrate -from %s -to <new.db>", path, path)
	}

	if _, err := db.Exec(storeSchema); err != nil {
		db.Close()
		return nil, err
	}

	return &accountStore{
		db:          db,
		hubVisits:   map[string]string{},
		sessions:    map[string]session{},
		oauthStates: map[string]oauthState{},
	}, nil
}

// isLegacySchema reports whether the database still uses the old layout,
// recognizable by an accounts table without an id column.
func isLegacySchema(db *sql.DB) (bool, error) {
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'accounts'").Scan(&n); err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('accounts') WHERE name = 'id'").Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}

func (s *accountStore) ensureAccount(email string) error {
	_, err := s.db.Exec("INSERT OR IGNORE INTO accounts(email) VALUES (?)", email)
	return err
}

func (s *accountStore) macs(email string) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT mac_address FROM macs
		JOIN accounts ON accounts.id = macs.account_id
		WHERE accounts.email = ?
		ORDER BY mac_address
	`, email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var macAddresses []string
	for rows.Next() {
		var mac string
		if err := rows.Scan(&mac); err != nil {
			return nil, err
		}
		macAddresses = append(macAddresses, mac)
	}
	return macAddresses, rows.Err()
}

func (s *accountStore) addMAC(email, mac string) error {
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO macs(account_id, mac_address)
		SELECT id, ? FROM accounts WHERE email = ?
	`, mac, email)
	return err
}

func (s *accountStore) removeMAC(email, mac string) error {
	_, err := s.db.Exec(`
		DELETE FROM macs
		WHERE mac_address = ? AND account_id IN (SELECT id FROM accounts WHERE email = ?)
	`, mac, email)
	return err
}

// macAssignments maps every registered MAC address to its account email,
// or to "" when more than one account claims the same MAC.
func (s *accountStore) macAssignments() (map[string]string, error) {
	rows, err := s.db.Query(`
		SELECT macs.mac_address, accounts.email FROM macs
		JOIN accounts ON accounts.id = macs.account_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	assignments := map[string]string{}
	for rows.Next() {
		var mac, email string
		if err := rows.Scan(&mac, &email); err != nil {
			return nil, err
		}
		if existing, ok := assignments[mac]; ok && existing != email {
			assignments[mac] = ""
			continue
		}
		assignments[mac] = email
	}
	return assignments, rows.Err()
}

func (s *accountStore) token(email string) (token, bool, error) {
	var t token
	err := s.db.QueryRow(`
		SELECT token_type, refresh_token, scope, expires_at FROM tokens
		JOIN accounts ON accounts.id = tokens.account_id
		WHERE accounts.email = ?
	`, email).Scan(&t.Type, &t.RefreshToken, &t.Scope, &t.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return token{}, false, nil
	}
	if err != nil {
		return token{}, false, err
	}
	return t, t.canAuthorize(), nil
}

func (s *accountStore) hasToken(email string) bool {
	_, ok, err := s.token(email)
	if err != nil {
		log.Printf("look up token for %s: %v", email, err)
	}
	return ok
}

func (s *accountStore) saveToken(email string, t token) error {
	if err := s.ensureAccount(email); err != nil {
		return err
	}
	_, err := s.db.Exec(`
		INSERT INTO tokens(account_id, token_type, refresh_token, scope, expires_at)
		SELECT id, ?, ?, ?, ? FROM accounts WHERE email = ?
		ON CONFLICT(account_id) DO UPDATE SET
			token_type = excluded.token_type,
			refresh_token = excluded.refresh_token,
			scope = excluded.scope,
			expires_at = excluded.expires_at
	`, t.Type, t.RefreshToken, t.Scope, t.ExpiresAt, email)
	return err
}

func (s *accountStore) rcProfileID(email string) (string, bool) {
	var id string
	err := s.db.QueryRow("SELECT rc_profile_id FROM accounts WHERE email = ?", email).Scan(&id)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("look up RC profile id for %s: %v", email, err)
		}
		return "", false
	}
	return id, id != ""
}

func (s *accountStore) setRCProfileID(email, rcProfileID string) error {
	if err := s.ensureAccount(email); err != nil {
		return err
	}
	_, err := s.db.Exec("UPDATE accounts SET rc_profile_id = ? WHERE email = ?", rcProfileID, email)
	return err
}

func (s *accountStore) lastHubVisit(email string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hubVisits[email]
}

func (s *accountStore) recordHubVisit(email, date string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hubVisits[email] = date
}
