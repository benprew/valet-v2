#!/usr/bin/env sh
set -eu

profile_base="${1:-/tmp/valet-kiosk-browser}"
url="${2:-https://10.100.0.3}"
browser="${3:-chromium-browser}"
log_file="${4:-/tmp/valet-kiosk-browser.log}"
tls_cert_spki="${5:-}"

# Signal any kiosk browser left over from a previous run. We match on the
# shared profile base, so this catches every per-run profile directory below
# it. Unlike before, we do NOT wait for the browser to exit: the new browser
# gets its own fresh profile directory (below), so there is no shared state to
# race over, and the old browser cleans up after itself when it finally dies.
pkill -f -- "--user-data-dir=${profile_base}" 2>/dev/null || true

# Best-effort sweep of profile directories orphaned by a browser that was hard
# killed (e.g. a reboot) before its self-cleanup could run.
rm -rf -- "${profile_base}".* 2>/dev/null || true

# Fresh, unique profile directory for this launch.
profile_dir="$(mktemp -d -- "${profile_base}.XXXXXX")"

# Launch detached. The browser runs inside a subshell that removes its own
# profile directory when it exits, so temp space never accumulates. All output
# is redirected to the log file (not the inherited stdout/stderr), so the
# parent process waiting on this script returns immediately instead of
# blocking for the browser's whole lifetime.
DISPLAY=:0 nohup sh -c '
    browser="$1"; profile_dir="$2"; url="$3"; tls_cert_spki="$4"
    trap "rm -rf -- \"$profile_dir\"" EXIT INT TERM
    set -- "$browser" \
        --kiosk \
        --no-first-run \
        --disable-session-crashed-bubble \
        --user-data-dir="$profile_dir"
    if [ -n "$tls_cert_spki" ]; then
        set -- "$@" "--ignore-certificate-errors-spki-list=$tls_cert_spki"
    fi
    "$@" "$url"
' valet-kiosk-browser "${browser}" "${profile_dir}" "${url}" "${tls_cert_spki}" \
    >"${log_file}" 2>&1 &
