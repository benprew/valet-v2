SHELL := /usr/bin/bash

.PHONY: build run test lint verify-systemd-service

# Build a fully static binary so it runs on older Linux distros with old
# glibc versions. The sqlite driver (modernc.org/sqlite) is pure Go, so
# disabling CGO removes the libc dependency entirely; no musl toolchain is
# needed. Before the switch from mattn/go-sqlite3, builds linked statically
# against musl instead.
#
# Cross-compile for another target with e.g.:
#   GOOS=linux GOARCH=arm64 make build
#
# OAuth credentials are stored passphrase-encrypted at rest in secrets.age and
# baked into the binary here so the deployed binary needs no flags/.env.
# Decrypt + inject via -ldflags. Plain `go build`/`go run .` skips all this.
build: .SHELLFLAGS := -euo pipefail -c
build:
	@echo "Enter the Recurse password to decrypt secrets"
	@secrets=$$(age -d secrets.age); \
	id=$$(grep "^CLIENT_ID=" <<<"$$secrets" | cut -d= -f2-); \
	secret=$$(grep "^CLIENT_SECRET=" <<<"$$secrets" | cut -d= -f2-); \
	if [ -z "$$id" ] || [ -z "$$secret" ]; then \
		echo "make build: secrets.age did not yield CLIENT_ID and CLIENT_SECRET" >&2; \
		exit 1; \
	fi; \
	CGO_ENABLED=0 go build \
		-ldflags "-X 'main.embeddedOAuthClientID=$$id' -X 'main.embeddedOAuthClientSecret=$$secret'" \
		.

run:
	./scripts/setup-arp-scan-capabilities.sh
	go run . $(VALET_FLAGS)

test:
	go test -count=10 ./...

lint:
	golangci-lint run --fix
	.venv/bin/ruff check --fix .
	.venv/bin/ruff format .
	.venv/bin/ty check .
