#!/usr/bin/env bash

# Build a fully static binary so it runs on older Linux distros with old
# glibc versions. The sqlite driver (modernc.org/sqlite) is pure Go, so
# disabling CGO removes the libc dependency entirely — no musl toolchain
# needed. (Before the switch from mattn/go-sqlite3, this script linked
# statically against musl instead.)
#
# Cross-compile for another target with e.g.:
#   GOOS=linux GOARCH=arm64 ./build.sh
CGO_ENABLED=0 go build .
