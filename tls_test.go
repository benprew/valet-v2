package main

import (
	"encoding/base64"
	"path/filepath"
	"testing"
)

func TestTLSCertificateSPKIHash(t *testing.T) {
	dir := t.TempDir()
	cfg, err := loadOrCreateTLSConfig(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	if err != nil {
		t.Fatal(err)
	}

	hash, err := tlsCertificateSPKIHash(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(hash)
	if err != nil {
		t.Fatalf("SPKI hash is not base64: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("expected SHA-256 hash, got %d bytes", len(decoded))
	}
}

func TestTLSCertificateSPKIHashRequiresCertificate(t *testing.T) {
	if _, err := tlsCertificateSPKIHash(nil); err == nil {
		t.Fatal("expected missing certificate to fail")
	}
}
