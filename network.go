package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

const arpScanInterval = 20 * time.Minute

// Both funcs are read-only: the arp-scan cache is refreshed out of band by
// deviceCache.start. They are vars so tests can stub the hub monitor and
// request/response paths independently.
var scanNetworkDevicesFunc = cachedNetworkDevices
var cachedNetworkDevicesFunc = cachedNetworkDevices
var arpScanFunc = arpScan

var deviceCache = cachedARPScan{
	interval: arpScanInterval,
}

type cachedARPScan struct {
	mu            sync.Mutex
	interval      time.Duration
	cachedDevices []networkDevice
}

type networkDevice struct {
	IP         string
	MAC        string
	Interface  string
	State      string
	Source     string
	Registered bool
}

// cachedNetworkDevices runs 'ip neigh' scan and combines that with results from
// the most recent arp-scan
func cachedNetworkDevices(ctx context.Context) ([]networkDevice, error) {
	cached := deviceCache.cached()
	neighbors, err := scanIPNeigh(ctx)
	if err != nil {
		fmt.Println("ERROR: ip neigh failed:", err)
		return cached, err
	}
	return mergeNetworkDevices(cached, neighbors), nil
}

// ip neigh returns ipv4 and ipv6 devices
func scanIPNeigh(ctx context.Context) ([]networkDevice, error) {
	out, err := exec.CommandContext(ctx, "ip", "neigh", "show").Output()
	if err != nil {
		return nil, err
	}

	var devices []networkDevice
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}

		device := networkDevice{
			IP:     fields[0],
			Source: "ip-neigh",
		}
		for i := 1; i < len(fields); i++ {
			switch fields[i] {
			case "dev":
				if i+1 < len(fields) {
					device.Interface = fields[i+1]
					i++
				}
			case "lladdr":
				if i+1 < len(fields) {
					device.MAC = strings.ToLower(fields[i+1])
					i++
				}
			default:
				device.State = fields[i]
			}
		}

		if isUsableNeighbor(device) {
			devices = append(devices, device)
		}
	}
	return devices, scanner.Err()
}

// start launches a background goroutine that refreshes the arp-scan cache
// every interval until ctx is cancelled.
func (c *cachedARPScan) start(ctx context.Context) {
	go func() {
		log.Printf("network scanner refreshing arp-scan cache every %s", c.interval)
		c.refreshWithTimeout(ctx)

		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.refreshWithTimeout(ctx)
			}
		}
	}()
}

func (c *cachedARPScan) refreshWithTimeout(ctx context.Context) {
	scanCtx, cancel := context.WithTimeout(ctx, c.interval)
	defer cancel()
	if err := c.refresh(scanCtx); err != nil {
		log.Printf("arp-scan refresh failed: %v", err)
	}
}

// refresh runs arp-scan and returns the cached devices. The mutex is never
// held across the arp-scan exec, so a concurrent cached() read never blocks
// on network probing.
func (c *cachedARPScan) refresh(ctx context.Context) error {
	devices, err := arpScanFunc(ctx)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(devices) > 0 {
		c.cachedDevices = cloneNetworkDevices(devices)
	}
	return nil
}

// cached returns the most recent arp-scan results without probing the network.
func (c *cachedARPScan) cached() []networkDevice {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneNetworkDevices(c.cachedDevices)
}

func arpScan(ctx context.Context) ([]networkDevice, error) {
	cmd := exec.CommandContext(ctx, arpScanPath(), "--localnet", "--quiet", "--plain", "--numeric")
	out, commandErr := cmd.CombinedOutput()
	devices, parseErr := arpScanToDevices(out)
	if parseErr != nil {
		return devices, parseErr
	}
	return devices, commandErr
}

func arpScanToDevices(out []byte) ([]networkDevice, error) {
	devMap := map[string]string{} // map of MAC -> IP
	var devices []networkDevice
	scanner := bufio.NewScanner(strings.NewReader(string(out)))

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		if net.ParseIP(fields[0]) == nil || !isMACAddress(fields[1]) {
			continue
		}
		devMap[strings.ToLower(fields[1])] = fields[0]
	}

	for mac, ip := range devMap {
		devices = append(devices, networkDevice{MAC: mac, IP: ip, Source: "arp-scan"})
	}
	sortDevices(devices)

	return devices, scanner.Err()
}

func arpScanPath() string {
	path, err := exec.LookPath("arp-scan")
	if err == nil {
		return path
	}

	return "/usr/sbin/arp-scan"
}

func isUsableNeighbor(device networkDevice) bool {
	if !isMACAddress(device.MAC) {
		return false
	}
	state := strings.ToUpper(device.State)
	return state != "FAILED" && state != "INCOMPLETE"
}

func isMACAddress(value string) bool {
	_, err := net.ParseMAC(value)
	return err == nil
}

func mergeNetworkDevices(primary, extra []networkDevice) []networkDevice {
	merged := cloneNetworkDevices(primary)
	seen := map[string]struct{}{}
	for _, device := range merged {
		seen[networkDeviceKey(device)] = struct{}{}
	}
	for _, device := range extra {
		key := networkDeviceKey(device)
		if _, ok := seen[key]; ok {
			continue
		}
		merged = append(merged, device)
		seen[key] = struct{}{}
	}
	return merged
}

func networkDeviceKey(device networkDevice) string {
	return device.IP + "\x00" + device.MAC
}

func cloneNetworkDevices(devices []networkDevice) []networkDevice {
	if len(devices) == 0 {
		return nil
	}
	return append([]networkDevice(nil), devices...)
}

func sortDevices(devices []networkDevice) {
	sort.Slice(devices, func(i, j int) bool {
		left := net.ParseIP(devices[i].IP)
		right := net.ParseIP(devices[j].IP)
		if left == nil || right == nil {
			if devices[i].IP == devices[j].IP {
				return devices[i].MAC < devices[j].MAC
			}
			return devices[i].IP < devices[j].IP
		}
		if cmp := bytes.Compare(left.To16(), right.To16()); cmp != 0 {
			return cmp < 0
		}
		return devices[i].MAC < devices[j].MAC
	})
}
