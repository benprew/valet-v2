package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestScanARPLocalNetParsesIPMACRows(t *testing.T) {
	devices, err := arpScanToDevices([]byte(`Interface: wlan0, type: EN10MB, MAC: 82:00:3b:d0:93:12, IPv4: 10.100.0.2
Starting arp-scan 1.10.0 with 4096 hosts
10.100.0.1 00:08:a2:0e:bc:61 Router
10.100.1.171 1C:69:7A:6F:44:1B Device
10.100.1.171 bc:17:b8:86:51:50 Device

3 packets received by filter
`))
	if err != nil {
		t.Fatalf("parse arp-scan output: %v", err)
	}

	expected := []networkDevice{
		{IP: "10.100.0.1", MAC: "00:08:a2:0e:bc:61", Source: "arp-scan"},
		{IP: "10.100.1.171", MAC: "1c:69:7a:6f:44:1b", Source: "arp-scan"},
		{IP: "10.100.1.171", MAC: "bc:17:b8:86:51:50", Source: "arp-scan"},
	}
	if !reflect.DeepEqual(devices, expected) {
		t.Fatalf("unexpected devices:\n got: %#v\nwant: %#v", devices, expected)
	}
}

func TestScanARPLocalNetReturnsParsedRowsWithCommandError(t *testing.T) {
	dir := t.TempDir()
	arpScanBin := filepath.Join(dir, "arp-scan")
	if err := os.WriteFile(arpScanBin, []byte("#!/usr/bin/env sh\necho '10.100.0.1 00:08:a2:0e:bc:61'\nexit 42\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	devices, err := arpScan(context.Background())
	if err == nil {
		t.Fatal("expected command error")
	}

	expected := []networkDevice{{IP: "10.100.0.1", MAC: "00:08:a2:0e:bc:61", Source: "arp-scan"}}
	if !reflect.DeepEqual(devices, expected) {
		t.Fatalf("unexpected parsed devices after command error:\n got: %#v\nwant: %#v", devices, expected)
	}
}

func TestStartRefreshesCacheEveryInterval(t *testing.T) {
	scans := make(chan struct{}, 1)
	restore := replaceARPScanForTest(t, func(context.Context) ([]networkDevice, error) {
		select {
		case scans <- struct{}{}:
		default:
		}
		return []networkDevice{{IP: "10.100.0.1", MAC: "00:08:a2:0e:bc:61", Source: "arp-scan"}}, nil
	})
	defer restore()

	cache := cachedARPScan{interval: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cache.start(ctx)

	// One immediate refresh plus at least one ticker-driven refresh.
	for i := 0; i < 2; i++ {
		select {
		case <-scans:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for arp-scan refresh %d", i+1)
		}
	}

	expected := []networkDevice{{IP: "10.100.0.1", MAC: "00:08:a2:0e:bc:61", Source: "arp-scan"}}
	if got := cache.cached(); !reflect.DeepEqual(got, expected) {
		t.Fatalf("cached() = %#v, want %#v", got, expected)
	}
}

func TestCachedARPScanIgnoresPartialResultsOnFailure(t *testing.T) {
	calls := 0
	restore := replaceARPScanForTest(t, func(context.Context) ([]networkDevice, error) {
		calls++
		switch calls {
		case 1:
			return []networkDevice{{IP: "10.100.0.1", MAC: "00:08:a2:0e:bc:61", Source: "arp-scan"}}, nil
		case 2:
			return []networkDevice{{IP: "10.100.0.2", MAC: "d8:3a:dd:1f:a1:9b", Source: "arp-scan"}}, errors.New("context deadline exceeded")
		default:
			t.Fatalf("unexpected arp-scan call %d", calls)
			return nil, nil
		}
	})
	defer restore()

	cache := cachedARPScan{interval: 10 * time.Minute}
	if err := cache.refresh(context.Background()); err != nil {
		t.Fatalf("first scan failed: %v", err)
	}

	if err := cache.refresh(context.Background()); err == nil {
		t.Fatal("expected partial scan to return command error")
	}

	expected := []networkDevice{{IP: "10.100.0.1", MAC: "00:08:a2:0e:bc:61", Source: "arp-scan"}}
	if cached := cache.cached(); !reflect.DeepEqual(cached, expected) {
		t.Fatalf("expected failed scan to keep previous cache:\n got: %#v\nwant: %#v", cached, expected)
	}
}

func TestCachedARPScanKeepsLastSuccessAfterFailure(t *testing.T) {
	calls := 0
	restore := replaceARPScanForTest(t, func(context.Context) ([]networkDevice, error) {
		calls++
		if calls == 1 {
			return []networkDevice{{IP: "10.100.0.1", MAC: "00:08:a2:0e:bc:61", Source: "arp-scan"}}, nil
		}
		return nil, errors.New("arp-scan unavailable")
	})
	defer restore()

	cache := cachedARPScan{interval: 10 * time.Minute}
	if err := cache.refresh(context.Background()); err != nil {
		t.Fatalf("first scan failed: %v", err)
	}

	if err := cache.refresh(context.Background()); err == nil {
		t.Fatal("expected second scan to return an error")
	}
	expected := []networkDevice{{IP: "10.100.0.1", MAC: "00:08:a2:0e:bc:61", Source: "arp-scan"}}
	if cached := cache.cached(); !reflect.DeepEqual(cached, expected) {
		t.Fatalf("unexpected cached devices after failure:\n got: %#v\nwant: %#v", cached, expected)
	}
}

func TestCachedReturnsLastRefreshWithoutScanning(t *testing.T) {
	calls := 0
	restore := replaceARPScanForTest(t, func(context.Context) ([]networkDevice, error) {
		calls++
		return []networkDevice{{IP: "10.100.0.1", MAC: "00:08:a2:0e:bc:61", Source: "arp-scan"}}, nil
	})
	defer restore()

	cache := cachedARPScan{interval: 10 * time.Minute}

	// cached() must never trigger a scan, even before the cache is primed.
	if got := cache.cached(); got != nil {
		t.Fatalf("cached() returned %#v before any refresh", got)
	}
	if calls != 0 {
		t.Fatalf("cached() ran arp-scan %d times; it must never probe the network", calls)
	}

	if err := cache.refresh(context.Background()); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}

	expected := []networkDevice{{IP: "10.100.0.1", MAC: "00:08:a2:0e:bc:61", Source: "arp-scan"}}
	for i := 0; i < 3; i++ {
		if got := cache.cached(); !reflect.DeepEqual(got, expected) {
			t.Fatalf("cached() = %#v, want %#v", got, expected)
		}
	}
	if calls != 1 {
		t.Fatalf("cached() reads triggered extra arp-scan calls: got %d, want 1", calls)
	}
}

func TestMergeNetworkDevicesDeduplicatesIPMACPairs(t *testing.T) {
	merged := mergeNetworkDevices(
		[]networkDevice{{IP: "10.100.0.1", MAC: "00:08:a2:0e:bc:61", Source: "ip-neigh"}},
		[]networkDevice{
			{IP: "10.100.0.1", MAC: "00:08:a2:0e:bc:61", Source: "arp-scan"},
			{IP: "10.100.0.2", MAC: "d8:3a:dd:1f:a1:9b", Source: "arp-scan"},
		},
	)

	expected := []networkDevice{
		{IP: "10.100.0.1", MAC: "00:08:a2:0e:bc:61", Source: "ip-neigh"},
		{IP: "10.100.0.2", MAC: "d8:3a:dd:1f:a1:9b", Source: "arp-scan"},
	}
	if !reflect.DeepEqual(merged, expected) {
		t.Fatalf("unexpected merged devices:\n got: %#v\nwant: %#v", merged, expected)
	}
}

func replaceARPScanForTest(t *testing.T, replacement func(context.Context) ([]networkDevice, error)) func() {
	t.Helper()

	original := arpScanFunc
	arpScanFunc = replacement
	return func() {
		arpScanFunc = original
	}
}
