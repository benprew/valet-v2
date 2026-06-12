#!/usr/bin/env sh
set -eu

set -x

profile_dir="${1:-/tmp/valet-kiosk-browser}"
url="${2:-http://127.0.0.1:3000}"
browser="${3:-chromium-browser}"
log_file="${4:-/tmp/valet-kiosk-browser.log}"

pkill -f -- "--user-data-dir=${profile_dir}" 2>/dev/null || true
rm -rf -- "${profile_dir}"
mkdir -p -- "${profile_dir}"

nohup "${browser}" \
    --no-first-run \
    --disable-session-crashed-bubble \
    --user-data-dir="${profile_dir}" \
    "${url}" >"${log_file}" 2>&1 &
