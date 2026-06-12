package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenStoreInitializesNewHubMetadataMaps(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "accounts.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if store.HubVisits == nil {
		t.Fatal("HubVisits map was nil")
	}
	if store.OAuthTokens == nil {
		t.Fatal("OAuthTokens map was nil")
	}
	if store.RCProfileIDs == nil {
		t.Fatal("RCProfileIDs map was nil")
	}
}

func TestOpenStoreMigratesLegacyJSONData(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "accounts.db")
	if err := os.WriteFile(filepath.Join(dir, "accounts.json"), []byte(`{
		"accounts":{"ben@example.com":["82:00:3b:d0:93:12"]},
		"hub_visits":{"ben@example.com":"2026-06-08"},
		"oauth_tokens":{"ben@example.com":{"access_token":"test-token"}},
		"rc_profile_ids":{"ben@example.com":"123"}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := openStore(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.db.Close(); err != nil {
		t.Fatalf("close migrated store: %v", err)
	}

	store, err = openStore(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	if got := store.Accounts["ben@example.com"]; len(got) != 1 || got[0] != "82:00:3b:d0:93:12" {
		t.Fatalf("expected migrated account MAC, got %#v", got)
	}
	if got := store.HubVisits["ben@example.com"]; got != "2026-06-08" {
		t.Fatalf("expected migrated hub visit, got %q", got)
	}
	if token := store.OAuthTokens["ben@example.com"]; token.AccessToken != "test-token" {
		t.Fatalf("expected migrated OAuth token, got %#v", token)
	}
	if got := store.RCProfileIDs["ben@example.com"]; got != "123" {
		t.Fatalf("expected migrated RC profile id, got %q", got)
	}
}
