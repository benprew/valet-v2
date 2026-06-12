package main

import (
	"path/filepath"
	"testing"
)

func TestStoreRoundTripsAccountData(t *testing.T) {
	const email = "ben@example.com"
	path := filepath.Join(t.TempDir(), "accounts.db")

	store, err := openStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.ensureAccount(email); err != nil {
		t.Fatal(err)
	}
	for _, mac := range []string{"82:00:3b:d0:93:13", "82:00:3b:d0:93:12", "82:00:3b:d0:93:12"} {
		if err := store.addMAC(email, mac); err != nil {
			t.Fatal(err)
		}
	}
	saved := token{Type: tokenTypeOAuth, RefreshToken: "refresh", Scope: "hub_visits", ExpiresAt: "2026-06-09T05:35:14Z"}
	if err := store.saveToken(email, saved); err != nil {
		t.Fatal(err)
	}
	if err := store.setRCProfileID(email, "123"); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = openStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	macs, err := store.macs(email)
	if err != nil {
		t.Fatal(err)
	}
	if len(macs) != 2 || macs[0] != "82:00:3b:d0:93:12" || macs[1] != "82:00:3b:d0:93:13" {
		t.Fatalf("unexpected MAC addresses: %#v", macs)
	}
	got, ok, err := store.token(email)
	if err != nil || !ok {
		t.Fatalf("expected stored token, got ok=%v err=%v", ok, err)
	}
	if got != saved {
		t.Fatalf("token round trip mismatch: %#v", got)
	}
	if id, ok := store.rcProfileID(email); !ok || id != "123" {
		t.Fatalf("expected RC profile id 123, got %q (ok=%v)", id, ok)
	}
}

func TestStoreSaveTokenReplacesExistingToken(t *testing.T) {
	const email = "ben@example.com"
	store := testStore(t)

	if err := store.saveToken(email, token{Type: tokenTypeOAuth, RefreshToken: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := store.saveToken(email, token{Type: tokenTypePAT, RefreshToken: "second"}); err != nil {
		t.Fatal(err)
	}

	got, ok, err := store.token(email)
	if err != nil || !ok {
		t.Fatalf("expected stored token, got ok=%v err=%v", ok, err)
	}
	if got.Type != tokenTypePAT || got.RefreshToken != "second" {
		t.Fatalf("expected replaced token, got %#v", got)
	}
}

func TestStoreRemoveMAC(t *testing.T) {
	const email = "ben@example.com"
	store := testStore(t)

	if err := store.ensureAccount(email); err != nil {
		t.Fatal(err)
	}
	if err := store.addMAC(email, "82:00:3b:d0:93:12"); err != nil {
		t.Fatal(err)
	}
	if err := store.removeMAC(email, "82:00:3b:d0:93:12"); err != nil {
		t.Fatal(err)
	}

	macs, err := store.macs(email)
	if err != nil {
		t.Fatal(err)
	}
	if len(macs) != 0 {
		t.Fatalf("expected no MAC addresses, got %#v", macs)
	}
}

func TestMACAssignmentsFlagAmbiguousMACs(t *testing.T) {
	const mac = "82:00:3b:d0:93:12"
	store := testStore(t)

	for _, email := range []string{"one@example.com", "two@example.com"} {
		if err := store.ensureAccount(email); err != nil {
			t.Fatal(err)
		}
		if err := store.addMAC(email, mac); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.ensureAccount("three@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := store.addMAC("three@example.com", "82:00:3b:d0:93:13"); err != nil {
		t.Fatal(err)
	}

	assignments, err := store.macAssignments()
	if err != nil {
		t.Fatal(err)
	}
	if got := assignments[mac]; got != "" {
		t.Fatalf("expected ambiguous MAC to map to empty email, got %q", got)
	}
	if got := assignments["82:00:3b:d0:93:13"]; got != "three@example.com" {
		t.Fatalf("expected unambiguous MAC assignment, got %q", got)
	}
}

func TestHubVisitsAreNotPersisted(t *testing.T) {
	const email = "ben@example.com"
	path := filepath.Join(t.TempDir(), "accounts.db")

	store, err := openStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	store.recordHubVisit(email, "2026-06-08")
	if got := store.lastHubVisit(email); got != "2026-06-08" {
		t.Fatalf("expected recorded hub visit, got %q", got)
	}
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = openStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	if got := store.lastHubVisit(email); got != "" {
		t.Fatalf("expected hub visit to reset on restart, got %q", got)
	}
}

func TestOpenStoreRejectsLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.db")

	store, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		DROP TABLE macs; DROP TABLE tokens; DROP TABLE accounts;
		CREATE TABLE accounts (email TEXT PRIMARY KEY);
	`); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := openStore(path); err == nil {
		t.Fatal("expected legacy database to be rejected")
	}
}
