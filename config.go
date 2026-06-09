package main

import (
	"os"
	"strings"
)

const defaultRCBaseURL = "https://www.recurse.com"

func rcBaseURLFromEnv() string {
	baseURL := strings.TrimRight(os.Getenv("VALET_RC_BASE_URL"), "/")
	if baseURL == "" {
		return defaultRCBaseURL
	}
	return baseURL
}
