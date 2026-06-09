#!/usr/bin/env bash
set -euo pipefail

required_caps="cap_net_raw,cap_net_admin=ep"

if ! command -v arp-scan >/dev/null 2>&1; then
	echo "arp-scan is not installed or is not on PATH" >&2
	exit 1
fi

if ! command -v getcap >/dev/null 2>&1; then
	echo "getcap is required to inspect arp-scan capabilities" >&2
	exit 1
fi

if ! command -v setcap >/dev/null 2>&1; then
	echo "setcap is required to update arp-scan capabilities" >&2
	exit 1
fi

run_setcap() {
	if [[ "$EUID" -eq 0 ]]; then
		setcap "$required_caps" "$arp_scan_path"
		return
	fi

	if ! command -v sudo >/dev/null 2>&1; then
		echo "sudo is required to run setcap as a non-root user" >&2
		exit 1
	fi
	sudo setcap "$required_caps" "$arp_scan_path"
}

arp_scan_path="$(command -v arp-scan)"
current_caps="$(getcap -- "$arp_scan_path" | awk '{$1=""; sub(/^ /, ""); print}')"

has_required_caps() {
	local caps="$1"

	[[ "$caps" == *cap_net_raw* ]] &&
		[[ "$caps" == *cap_net_admin* ]] &&
		[[ "$caps" == *=*e* ]] &&
		[[ "$caps" == *=*p* ]]
}

if has_required_caps "$current_caps"; then
	echo "$arp_scan_path already has required capabilities: $current_caps"
	exit 0
fi

echo "Setting $required_caps on $arp_scan_path"
run_setcap

updated_caps="$(getcap -- "$arp_scan_path" | awk '{$1=""; sub(/^ /, ""); print}')"
if ! has_required_caps "$updated_caps"; then
	echo "failed to set required capabilities on $arp_scan_path; current capabilities: $updated_caps" >&2
	exit 1
fi

echo "$arp_scan_path capabilities: $updated_caps"
