package main

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	emailRE = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	macRE   = regexp.MustCompile(`(?i)^([0-9a-f]{2}:){5}[0-9a-f]{2}$`)
)

func normalizeEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !emailRE.MatchString(email) {
		return "", fmt.Errorf("invalid email address")
	}
	return email, nil
}

func normalizeMAC(mac string) (string, error) {
	mac = strings.ToLower(strings.TrimSpace(mac))
	mac = strings.ReplaceAll(mac, "-", ":")
	if !macRE.MatchString(mac) {
		return "", fmt.Errorf("invalid MAC address")
	}
	return mac, nil
}

func contains(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

func cloneStrings(values []string) []string {
	out := append([]string(nil), values...)
	return out
}
