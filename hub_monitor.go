package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
	_ "time/tzdata"
)

const defaultHubCheckInterval = time.Minute
const defaultHubScanTimeout = 6 * time.Minute
const hubTimeZone = "America/New_York"

var hubLocation = loadHubLocation()

type hubMonitorConfig struct {
	BaseURL     string
	Interval    time.Duration
	ScanTimeout time.Duration
}

func currentHubMonitorConfig() hubMonitorConfig {
	return hubMonitorConfig{
		BaseURL:     rcBaseURL(),
		Interval:    conf.HubCheckInterval,
		ScanTimeout: conf.HubScanTimeout,
	}
}

func normalizeHubMonitorConfig(cfg hubMonitorConfig) hubMonitorConfig {
	if cfg.Interval <= 0 {
		cfg.Interval = defaultHubCheckInterval
	}
	if cfg.ScanTimeout <= 0 {
		cfg.ScanTimeout = defaultHubScanTimeout
	}
	return cfg
}

func (s *accountStore) startHubMonitor(ctx context.Context, cfg hubMonitorConfig) {
	cfg = normalizeHubMonitorConfig(cfg)

	client := newHubVisitClient(cfg)
	go func() {
		log.Printf("hub monitor checking local devices every %s with %s scan timeout", cfg.Interval, cfg.ScanTimeout)
		s.runHubMonitorScan(ctx, client, cfg.ScanTimeout)

		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runHubMonitorScan(ctx, client, cfg.ScanTimeout)
			}
		}
	}()
}

func (s *accountStore) runHubMonitorScan(ctx context.Context, client *hubVisitClient, timeout time.Duration) {
	if timeout <= 0 {
		timeout = defaultHubScanTimeout
	}
	scanCtx, cancel := context.WithTimeout(ctx, timeout)
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

	seenDevices := map[string][]networkDevice{}
	for _, device := range devices {
		seenDevices[device.MAC] = append(seenDevices[device.MAC], device)
	}

	assignments, err := s.macAssignments()
	if err != nil {
		return err
	}

	today := hubDate(now)
	var errs []error
	for mac, email := range assignments {
		if email == "" {
			log.Printf("hub monitor skipping %s: assigned to multiple emails", mac)
			continue
		}
		candidates, seen := seenDevices[mac]
		if !seen {
			continue
		}
		if s.lastHubVisit(email) == today {
			continue
		}
		if !s.hasToken(email) {
			continue
		}
		// Unless a fresh REACHABLE neighbor proves it, actively probe to
		// confirm the device hasn't left behind a STALE entry.
		if !hasStrongPresence(candidates) {
			present, err := verifyDevicePresentFunc(ctx, candidates)
			if err != nil {
				errs = append(errs, fmt.Errorf("verify %s for %s: %w", mac, email, err))
				continue
			}
			if !present {
				log.Printf("hub monitor skipping %s: %s did not answer ARP probe (stale neighbor entry)", email, mac)
				continue
			}
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

func (s *accountStore) markEmailInHub(ctx context.Context, client *hubVisitClient, email, date string) error {
	bearer, err := s.bearerToken(ctx, email)
	if err != nil {
		return err
	}
	client = client.withToken(bearer)

	rcProfileID, ok := s.rcProfileID(email)
	if !ok {
		rcProfileID, err = client.findRCProfileID(ctx, email)
		if err != nil {
			return err
		}
		if err := s.setRCProfileID(email, rcProfileID); err != nil {
			return fmt.Errorf("cache RC profile id: %w", err)
		}
	}

	if err := client.markHubVisit(ctx, rcProfileID, date); err != nil {
		return err
	}
	s.recordHubVisit(email, date)
	return nil
}
