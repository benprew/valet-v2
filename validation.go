package main

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

var (
	emailRE     = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	rawHexMACRE = regexp.MustCompile(`(?i)^[0-9a-f]{12}$`)
)

func normalizeEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !emailRE.MatchString(email) {
		return "", fmt.Errorf("invalid email address")
	}
	return email, nil
}

func normalizeMAC(mac string) (string, error) {
	mac = strings.TrimSpace(mac)
	if rawHexMACRE.MatchString(mac) {
		mac = strings.Join([]string{
			mac[0:2],
			mac[2:4],
			mac[4:6],
			mac[6:8],
			mac[8:10],
			mac[10:12],
		}, ":")
	}

	hardwareAddr, err := net.ParseMAC(mac)
	if err != nil || len(hardwareAddr) != 6 {
		return "", fmt.Errorf("invalid MAC address")
	}
	return hardwareAddr.String(), nil
}

func contains(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}
