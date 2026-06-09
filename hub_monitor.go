package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"
	_ "time/tzdata"
)

const defaultHubCheckInterval = time.Minute
const hubTimeZone = "America/New_York"

var hubLocation = loadHubLocation()

type hubMonitorConfig struct {
	BaseURL  string
	Interval time.Duration
}

func hubMonitorConfigFromEnv() hubMonitorConfig {
	interval := defaultHubCheckInterval
	if raw := os.Getenv("VALET_HUB_CHECK_INTERVAL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			log.Printf("invalid VALET_HUB_CHECK_INTERVAL %q, using %s", raw, interval)
		} else if parsed > 0 {
			interval = parsed
		}
	}

	return hubMonitorConfig{
		BaseURL:  rcBaseURLFromEnv(),
		Interval: interval,
	}
}

func (s *accountStore) startHubMonitor(ctx context.Context, cfg hubMonitorConfig) {
	if cfg.Interval <= 0 {
		cfg.Interval = defaultHubCheckInterval
	}

	client := newHubVisitClient(cfg)
	go func() {
		log.Printf("hub monitor checking local devices every %s", cfg.Interval)
		s.runHubMonitorScan(ctx, client)

		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runHubMonitorScan(ctx, client)
			}
		}
	}()
}

func (s *accountStore) runHubMonitorScan(ctx context.Context, client *hubVisitClient) {
	scanCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	if err := s.runHubMonitorOnce(scanCtx, client, time.Now()); err != nil {
		log.Printf("hub monitor scan failed: %v", err)
	}
}

func (s *accountStore) runHubMonitorOnce(ctx context.Context, client *hubVisitClient, now time.Time) error {
	devices, err := scanNetworkDevicesFunc(ctx)
	if err != nil {
		return err
	}

	seenMACs := map[string]struct{}{}
	for _, device := range devices {
		seenMACs[device.MAC] = struct{}{}
	}

	assignments := s.macAssignments()
	today := hubDate(now)
	var errs []error
	for mac, email := range assignments {
		if email == "" {
			log.Printf("hub monitor skipping %s: assigned to multiple emails", mac)
			continue
		}
		if _, seen := seenMACs[mac]; !seen {
			continue
		}
		if s.lastHubVisit(email) == today {
			continue
		}
		if !s.hasOAuthToken(email) {
			continue
		}
		if err := s.markEmailInHub(ctx, client, email, today); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", email, err))
			continue
		}
		log.Printf("marked %s in the Hub for %s after seeing %s", email, today, mac)
	}
	return errors.Join(errs...)
}

func loadHubLocation() *time.Location {
	location, err := time.LoadLocation(hubTimeZone)
	if err != nil {
		log.Printf("could not load %s timezone, using fixed EST: %v", hubTimeZone, err)
		return time.FixedZone("EST", -5*60*60)
	}
	return location
}

func hubDate(now time.Time) string {
	return now.In(hubLocation).Format(time.DateOnly)
}

func (s *accountStore) macAssignments() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()

	assignments := map[string]string{}
	for email, macAddresses := range s.Accounts {
		for _, mac := range macAddresses {
			if existing, ok := assignments[mac]; ok && existing != email {
				assignments[mac] = ""
				continue
			}
			assignments[mac] = email
		}
	}
	return assignments
}

func (s *accountStore) lastHubVisit(email string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.HubVisits[email]
}

func (s *accountStore) cachedRCProfileID(email string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.RCProfileIDs[email]
	return id, ok && id != ""
}

func (s *accountStore) cacheRCProfileID(email, rcProfileID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.RCProfileIDs[email] = rcProfileID
	return s.saveLocked()
}

func (s *accountStore) cacheAuthorizedRCProfileID(ctx context.Context, email, accessToken string) error {
	client := newHubVisitClient(hubMonitorConfigFromEnv()).withToken(accessToken)
	rcProfileID, err := client.findRCProfileID(ctx, email)
	if err != nil {
		return err
	}
	return s.cacheRCProfileID(email, rcProfileID)
}

func (s *accountStore) recordHubVisit(email, date string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.HubVisits[email] = date
	return s.saveLocked()
}

func (s *accountStore) markEmailInHub(ctx context.Context, client *hubVisitClient, email, date string) error {
	token, err := s.usableOAuthToken(ctx, email)
	if err != nil {
		return err
	}
	client = client.withToken(token.AccessToken)

	rcProfileID, ok := s.cachedRCProfileID(email)
	if !ok {
		var err error
		rcProfileID, err = client.findRCProfileID(ctx, email)
		if err != nil {
			return err
		}
		if err := s.cacheRCProfileID(email, rcProfileID); err != nil {
			return fmt.Errorf("cache RC profile id: %w", err)
		}
	}

	if err := client.markHubVisit(ctx, rcProfileID, date); err != nil {
		return err
	}
	if err := s.recordHubVisit(email, date); err != nil {
		return fmt.Errorf("record hub visit: %w", err)
	}
	return nil
}
