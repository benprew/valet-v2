#!/usr/bin/env sh
set -eu

set -x

profile_dir="${1:-/tmp/valet-kiosk-browser}"
url="${2:-http://127.0.0.1:3000}"
browser="${3:-chromium-browser}"
log_file="${4:-/tmp/valet-kiosk-browser.log}"

pkill -f -- "--user-data-dir=${profile_dir}" 2>/dev/null || true

# pkill only sends the signal; wait for the browser to actually exit before
# removing its profile, otherwise it keeps writing files into the directory
# and rm fails with "Directory not empty".
i=0
while pgrep -f -- "--user-data-dir=${profile_dir}" >/dev/null 2>&1; do
    i=$((i + 1))
    if [ "${i}" -ge 50 ]; then
        pkill -9 -f -- "--user-data-dir=${profile_dir}" 2>/dev/null || true
        break
    fi
    sleep 0.1
done

rm -rf -- "${profile_dir}"
mkdir -p -- "${profile_dir}"

DISPLAY=:0 nohup "${browser}" \
    --kiosk \
    --no-first-run \
    --disable-session-crashed-bubble \
    --user-data-dir="${profile_dir}" \
    "${url}" >"${log_file}" 2>&1 &
