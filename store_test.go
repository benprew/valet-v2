package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenStoreInitializesNewHubMetadataMaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	if err := os.WriteFile(path, []byte(`{"accounts":{"ben@example.com":["82:00:3b:d0:93:12"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := openStore(path)
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
