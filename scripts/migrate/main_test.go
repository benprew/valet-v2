package main

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrateConvertsLegacyDatabase(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.db")
	newPath := filepath.Join(dir, "new.db")

	old, err := sql.Open("sqlite", oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`
		CREATE TABLE accounts (email TEXT PRIMARY KEY);
		CREATE TABLE account_macs (email TEXT NOT NULL, mac_address TEXT NOT NULL, PRIMARY KEY (email, mac_address));
		CREATE TABLE hub_visits (email TEXT PRIMARY KEY, date TEXT NOT NULL);
		CREATE TABLE oauth_tokens (
			email TEXT PRIMARY KEY,
			access_token TEXT NOT NULL,
			token_type TEXT NOT NULL DEFAULT '',
			refresh_token TEXT NOT NULL DEFAULT '',
			scope TEXT NOT NULL DEFAULT '',
			expires_at TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE rc_profile_ids (email TEXT PRIMARY KEY, rc_profile_id TEXT NOT NULL);

		INSERT INTO accounts VALUES ('ben@example.com'), ('other@example.com'), ('pat@example.com'), ('badpat@example.com');
		INSERT INTO account_macs VALUES
			('ben@example.com', '82:00:3b:d0:93:12'),
			('ben@example.com', '82:00:3b:d0:93:13'),
			('pat@example.com', '82:00:3b:d0:93:14');
		INSERT INTO hub_visits VALUES ('ben@example.com', '2026-06-08');
		INSERT INTO oauth_tokens VALUES
			('ben@example.com', 'access', 'Bearer', 'refresh-token', 'hub_visits', '2026-06-09T05:35:14Z'),
			('other@example.com', 'access-only', 'Bearer', '', '', ''),
			('pat@example.com', 'legacy-pat-token', 'pat', '', '', ''),
			('badpat@example.com', '', 'pat', '', '', '');
		INSERT INTO rc_profile_ids VALUES ('ben@example.com', '123');
	`); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	if err := migrate(oldPath, newPath); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	db, err := sql.Open("sqlite", newPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var rcProfileID string
	if err := db.QueryRow("SELECT rc_profile_id FROM accounts WHERE email = 'ben@example.com'").Scan(&rcProfileID); err != nil {
		t.Fatalf("migrated account: %v", err)
	}
	if rcProfileID != "123" {
		t.Fatalf("expected rc_profile_id 123, got %q", rcProfileID)
	}

	macs, err := stringColumn(db, `
		SELECT mac_address FROM macs
		JOIN accounts ON accounts.id = macs.account_id
		WHERE accounts.email = 'ben@example.com'
		ORDER BY mac_address
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(macs) != 2 || macs[0] != "82:00:3b:d0:93:12" || macs[1] != "82:00:3b:d0:93:13" {
		t.Fatalf("unexpected migrated MACs: %#v", macs)
	}

	var tokenType, refreshToken, scope, expiresAt string
	if err := db.QueryRow(`
		SELECT token_type, refresh_token, scope, expires_at FROM tokens
		JOIN accounts ON accounts.id = tokens.account_id
		WHERE accounts.email = 'ben@example.com'
	`).Scan(&tokenType, &refreshToken, &scope, &expiresAt); err != nil {
		t.Fatalf("migrated token: %v", err)
	}
	if tokenType != "oauth" || refreshToken != "refresh-token" || scope != "hub_visits" || expiresAt != "2026-06-09T05:35:14Z" {
		t.Fatalf("unexpected migrated token: %q %q %q %q", tokenType, refreshToken, scope, expiresAt)
	}

	if err := db.QueryRow(`
		SELECT token_type, refresh_token FROM tokens
		JOIN accounts ON accounts.id = tokens.account_id
		WHERE accounts.email = 'pat@example.com'
	`).Scan(&tokenType, &refreshToken); err != nil {
		t.Fatalf("migrated PAT: %v", err)
	}
	if tokenType != "pat" || refreshToken != "legacy-pat-token" {
		t.Fatalf("unexpected migrated PAT: %q %q", tokenType, refreshToken)
	}

	var tokenCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM tokens").Scan(&tokenCount); err != nil {
		t.Fatal(err)
	}
	if tokenCount != 2 {
		t.Fatalf("expected two usable tokens to be migrated, got %d tokens", tokenCount)
	}
}
