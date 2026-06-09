package main

import (
	"bufio"
	"context"
	"log"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

const arpScanInterval = 10 * time.Minute

var scanNetworkDevicesFunc = scanNetworkDevices
var runARPScanCommandFunc = runARPScanCommand

var arpScanCache = cachedARPScan{
	interval: arpScanInterval,
}

type cachedARPScan struct {
	mu            sync.Mutex
	interval      time.Duration
	lastRun       time.Time
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

func scanNetworkDevices(ctx context.Context) ([]networkDevice, error) {
	devices, err := scanIPNeigh(ctx)
	if err != nil {
		devices, err = scanProcARP()
		if err != nil {
			return nil, err
		}
	}

	if arpDevices, err := arpScanCache.devices(ctx, time.Now()); len(arpDevices) > 0 {
		devices = mergeNetworkDevices(devices, arpDevices)
	} else if err != nil {
		log.Printf("arp-scan failed: %v", err)
	}

	sortDevices(devices)
	return devices, nil
}

func scanIPNeigh(ctx context.Context) ([]networkDevice, error) {
	out, err := exec.CommandContext(ctx, "ip", "-r", "neigh", "show").Output()
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
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	sortDevices(devices)
	return devices, nil
}

func scanProcARP() ([]networkDevice, error) {
	file, err := os.Open("/proc/net/arp")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var devices []networkDevice
	scanner := bufio.NewScanner(file)
	first := true
	for scanner.Scan() {
		if first {
			first = false
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 {
			continue
		}
		device := networkDevice{
			IP:        fields[0],
			MAC:       strings.ToLower(fields[3]),
			Interface: fields[5],
			Source:    "proc-arp",
		}
		if isUsableNeighbor(device) {
			devices = append(devices, device)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	sortDevices(devices)
	return devices, nil
}

func (c *cachedARPScan) devices(ctx context.Context, now time.Time) ([]networkDevice, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.lastRun.IsZero() && now.Sub(c.lastRun) < c.interval {
		return cloneNetworkDevices(c.cachedDevices), nil
	}
	c.lastRun = now

	devices, err := scanARPLocalNet(ctx)
	if err != nil {
		return cloneNetworkDevices(c.cachedDevices), err
	}
	c.cachedDevices = cloneNetworkDevices(devices)
	return cloneNetworkDevices(c.cachedDevices), nil
}

func scanARPLocalNet(ctx context.Context) ([]networkDevice, error) {
	out, err := runARPScanCommandFunc(ctx)
	if err != nil {
		return nil, err
	}

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

		device := networkDevice{
			IP:     fields[0],
			MAC:    strings.ToLower(fields[1]),
			Source: "arp-scan",
		}
		if isUsableNeighbor(device) {
			devices = append(devices, device)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	sortDevices(devices)
	return devices, nil
}

func runARPScanCommand(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "arp-scan", "--localnet").Output()
}

func isUsableNeighbor(device networkDevice) bool {
	if device.IP == "" || device.MAC == "" {
		return false
	}
	if device.MAC == "00:00:00:00:00:00" {
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
		if cmp := bytesCompare(left.To16(), right.To16()); cmp != 0 {
			return cmp < 0
		}
		return devices[i].MAC < devices[j].MAC
	})
}

func bytesCompare(a, b []byte) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return len(a) - len(b)
}
