package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestScanARPLocalNetParsesIPMACRows(t *testing.T) {
	restore := replaceARPScanCommandForTest(t, []byte(`Interface: wlan0, type: EN10MB, MAC: 82:00:3b:d0:93:12, IPv4: 10.100.0.2
Starting arp-scan 1.10.0 with 4096 hosts
10.100.0.1 00:08:a2:0e:bc:61 Router
10.100.1.171 1C:69:7A:6F:44:1B Device
10.100.1.171 bc:17:b8:86:51:50 Device

3 packets received by filter
`), nil)
	defer restore()

	devices, err := scanARPLocalNet(context.Background())
	if err != nil {
		t.Fatalf("scan arp local net: %v", err)
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

func TestCachedARPScanRunsEveryInterval(t *testing.T) {
	calls := 0
	restore := replaceARPScanCommandFuncForTest(t, func(context.Context) ([]byte, error) {
		calls++
		return []byte("10.100.0.1 00:08:a2:0e:bc:61\n"), nil
	})
	defer restore()

	cache := cachedARPScan{interval: 10 * time.Minute}
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)

	if _, err := cache.devices(context.Background(), now); err != nil {
		t.Fatalf("first scan failed: %v", err)
	}
	if _, err := cache.devices(context.Background(), now.Add(9*time.Minute)); err != nil {
		t.Fatalf("cached scan failed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected one arp-scan call before interval elapsed, got %d", calls)
	}

	if _, err := cache.devices(context.Background(), now.Add(10*time.Minute)); err != nil {
		t.Fatalf("second scan failed: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected second arp-scan call after interval elapsed, got %d", calls)
	}
}

func TestCachedARPScanKeepsLastSuccessAfterFailure(t *testing.T) {
	calls := 0
	restore := replaceARPScanCommandFuncForTest(t, func(context.Context) ([]byte, error) {
		calls++
		if calls == 1 {
			return []byte("10.100.0.1 00:08:a2:0e:bc:61\n"), nil
		}
		return nil, errors.New("arp-scan unavailable")
	})
	defer restore()

	cache := cachedARPScan{interval: 10 * time.Minute}
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	if _, err := cache.devices(context.Background(), now); err != nil {
		t.Fatalf("first scan failed: %v", err)
	}

	devices, err := cache.devices(context.Background(), now.Add(10*time.Minute))
	if err == nil {
		t.Fatal("expected second scan to return an error")
	}
	expected := []networkDevice{{IP: "10.100.0.1", MAC: "00:08:a2:0e:bc:61", Source: "arp-scan"}}
	if !reflect.DeepEqual(devices, expected) {
		t.Fatalf("unexpected cached devices after failure:\n got: %#v\nwant: %#v", devices, expected)
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

func replaceARPScanCommandForTest(t *testing.T, output []byte, err error) func() {
	t.Helper()

	return replaceARPScanCommandFuncForTest(t, func(context.Context) ([]byte, error) {
		return output, err
	})
}

func replaceARPScanCommandFuncForTest(t *testing.T, replacement func(context.Context) ([]byte, error)) func() {
	t.Helper()

	original := runARPScanCommandFunc
	runARPScanCommandFunc = replacement
	return func() {
		runARPScanCommandFunc = original
	}
}
