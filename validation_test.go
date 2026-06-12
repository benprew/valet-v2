package main

import "testing"

func TestNormalizeMACFormatsCanonicalString(t *testing.T) {
	tests := map[string]string{
		"82:00:3B:D0:93:12": "82:00:3b:d0:93:12",
		"82-00-3B-D0-93-12": "82:00:3b:d0:93:12",
		"8200.3BD0.9312":    "82:00:3b:d0:93:12",
		"82003BD09312":      "82:00:3b:d0:93:12",
		" 82003BD09312 ":    "82:00:3b:d0:93:12",
	}

	for input, want := range tests {
		got, err := normalizeMAC(input)
		if err != nil {
			t.Fatalf("normalizeMAC(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("normalizeMAC(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeMACRejectsInvalidValues(t *testing.T) {
	for _, input := range []string{
		"",
		"82:00:3b:d0:93",
		"82:00:3b:d0:93:12:34",
		"82:00:3b:d0:93:zz",
	} {
		if got, err := normalizeMAC(input); err == nil {
			t.Fatalf("normalizeMAC(%q) = %q, want error", input, got)
		}
	}
}
