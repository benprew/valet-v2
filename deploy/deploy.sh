#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
binary="$repo_root/valet-v2"
unit="$repo_root/deploy/valet-v2.service"
target="pirc@10.100.0.3"
remote_stage=""

if [[ ! -x "$binary" ]]; then
	printf 'deploy: %s does not exist or is not executable; run make build first\n' "$binary" >&2
	exit 1
fi

cleanup() {
	if [[ -n "$remote_stage" ]]; then
		ssh "$target" rm -rf -- "$remote_stage" >/dev/null 2>&1 || true
	fi
}
trap cleanup EXIT

remote_stage="$(ssh "$target" 'mktemp -d /tmp/valet-v2-deploy.XXXXXX')"
if [[ ! "$remote_stage" =~ ^/tmp/valet-v2-deploy\.[[:alnum:]]+$ ]]; then
	printf 'deploy: unexpected staging directory returned by %s: %q\n' "$target" "$remote_stage" >&2
	exit 1
fi

scp "$binary" "$unit" "$target:$remote_stage/"

ssh "$target" bash -s -- "$remote_stage" <<'REMOTE'
set -euo pipefail

stage="$1"
install_dir=/home/pirc/valet
service_name=valet-v2.service
binary_tmp="$install_dir/.valet-v2.deploy.$$"
unit_tmp="/etc/systemd/system/.valet-v2.service.deploy.$$"

cleanup() {
	sudo rm -f -- "$binary_tmp" "$unit_tmp"
}
trap cleanup EXIT

sudo install -d -o pirc -g pirc -m 0755 "$install_dir"
sudo install -o pirc -g pirc -m 0755 "$stage/valet-v2" "$binary_tmp"
sudo install -o root -g root -m 0644 "$stage/valet-v2.service" "$unit_tmp"
sudo mv -f -- "$binary_tmp" "$install_dir/valet-v2"
sudo mv -f -- "$unit_tmp" "/etc/systemd/system/$service_name"

sudo systemctl daemon-reload
if sudo systemctl is-enabled --quiet "$service_name"; then
	sudo systemctl restart "$service_name"
else
	sudo systemctl enable --now "$service_name"
fi
REMOTE

printf 'Deployed valet-v2 to %s\n' "$target"
