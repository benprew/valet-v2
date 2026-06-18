// Command migrate converts a V.A.L.E.T. SQLite database from the legacy
// per-email-table layout (accounts/account_macs/oauth_tokens/rc_profile_ids/
// hub_visits) to the accounts/macs/tokens schema.
//
// OAuth access tokens are no longer persisted, so only refresh tokens are
// migrated for OAuth rows. Legacy PAT rows keep the PAT from access_token.
// Tokens without a usable credential are skipped and those accounts must
// re-authorize. Hub visits are kept in memory by the server now and are
// dropped.
//
// Usage:
//
//	go run ./scripts/migrate -from data/accounts.db -to data/accounts-new.db
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"

	_ "modernc.org/sqlite"
)

const newSchema = `
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

func main() {
	log.SetFlags(0)
	from := flag.String("from", "data/accounts.db", "path to the old database")
	to := flag.String("to", "", "path for the new database (must not exist)")
	flag.Parse()

	if *to == "" {
		log.Fatal("-to is required")
	}
	if _, err := os.Stat(*from); err != nil {
		log.Fatalf("old database: %v", err)
	}
	if _, err := os.Stat(*to); err == nil {
		log.Fatalf("%s already exists", *to)
	}

	if err := migrate(*from, *to); err != nil {
		os.Remove(*to)
		log.Fatal(err)
	}
}

func migrate(fromPath, toPath string) error {
	db, err := sql.Open("sqlite", toPath)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(newSchema); err != nil {
		return fmt.Errorf("create new schema: %w", err)
	}
	if _, err := db.Exec("ATTACH DATABASE ? AS old", fromPath); err != nil {
		return fmt.Errorf("attach old database: %w", err)
	}

	// The old store keyed every table by email, so collect emails from all
	// of them in case a row exists without a matching accounts entry.
	if _, err := db.Exec(`
		INSERT INTO accounts(email, rc_profile_id)
		SELECT e.email, COALESCE((SELECT rc_profile_id FROM old.rc_profile_ids r WHERE r.email = e.email), '')
		FROM (
			SELECT email FROM old.accounts
			UNION SELECT email FROM old.account_macs
			UNION SELECT email FROM old.oauth_tokens
			UNION SELECT email FROM old.rc_profile_ids
		) e
	`); err != nil {
		return fmt.Errorf("migrate accounts: %w", err)
	}

	if _, err := db.Exec(`
		INSERT INTO macs(account_id, mac_address)
		SELECT a.id, m.mac_address
		FROM old.account_macs m JOIN accounts a ON a.email = m.email
	`); err != nil {
		return fmt.Errorf("migrate MAC addresses: %w", err)
	}

	if _, err := db.Exec(`
		INSERT INTO tokens(account_id, token_type, refresh_token, scope, expires_at)
		SELECT
			a.id,
			CASE lower(t.token_type) WHEN 'pat' THEN 'pat' ELSE 'oauth' END,
			CASE lower(t.token_type) WHEN 'pat' THEN t.access_token ELSE t.refresh_token END,
			t.scope,
			t.expires_at
		FROM old.oauth_tokens t JOIN accounts a ON a.email = t.email
		WHERE
			(lower(t.token_type) = 'pat' AND t.access_token != '')
			OR (lower(t.token_type) != 'pat' AND t.refresh_token != '')
	`); err != nil {
		return fmt.Errorf("migrate tokens: %w", err)
	}

	skipped, err := stringColumn(db, `
		SELECT email FROM old.oauth_tokens
		WHERE NOT (
			(lower(token_type) = 'pat' AND access_token != '')
			OR (lower(token_type) != 'pat' AND refresh_token != '')
		)
	`)
	if err != nil {
		return err
	}
	for _, email := range skipped {
		log.Printf("skipped token for %s: no usable credential, account must re-authorize", email)
	}

	for _, table := range []struct{ label, query string }{
		{"accounts", "SELECT COUNT(*) FROM accounts"},
		{"MAC addresses", "SELECT COUNT(*) FROM macs"},
		{"tokens", "SELECT COUNT(*) FROM tokens"},
		{"hub visits dropped (kept in memory now)", "SELECT COUNT(*) FROM old.hub_visits"},
	} {
		var n int
		if err := db.QueryRow(table.query).Scan(&n); err != nil {
			return err
		}
		log.Printf("%d %s", n, table.label)
	}

	if _, err := db.Exec("DETACH DATABASE old"); err != nil {
		return err
	}
	log.Printf("migrated %s -> %s", fromPath, toPath)
	return nil
}

func stringColumn(db *sql.DB, query string) ([]string, error) {
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
