#!/usr/bin/env bash
set -euo pipefail

# Build a fully static binary so it runs on older Linux distros with old
# glibc versions. The sqlite driver (modernc.org/sqlite) is pure Go, so
# disabling CGO removes the libc dependency entirely — no musl toolchain
# needed. (Before the switch from mattn/go-sqlite3, this script linked
# statically against musl instead.)
#
# Cross-compile for another target with e.g.:
#   GOOS=linux GOARCH=arm64 ./build.sh
#
# OAuth credentials are stored encrypted at rest in secrets.age and baked
# into the binary here so the deployed binary needs no flags/.env. The age
# private key lives outside the repo (default: ~/.config/valet/build-key.txt,
# override with VALET_AGE_KEY). Decrypt + inject via -ldflags. Plain
# `go build`/`go run .` skips all this.

echo "Enter the Recurse password to decrypt secrets"
secrets=$(age -d secrets.age)
id=$(grep '^CLIENT_ID=' <<<"$secrets" | cut -d= -f2-)
secret=$(grep '^CLIENT_SECRET=' <<<"$secrets" | cut -d= -f2-)

if [ -z "$id" ] || [ -z "$secret" ]; then
  echo "build.sh: secrets.age did not yield CLIENT_ID and CLIENT_SECRET" >&2
  exit 1
fi

CGO_ENABLED=0 go build \
  -ldflags "-X 'main.embeddedOAuthClientID=${id}' -X 'main.embeddedOAuthClientSecret=${secret}'" \
  .
