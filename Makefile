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
	go run . $(VALET_FLAGS)

test: verify-systemd-service
	go test -count=10 ./...

verify-systemd-service:
	@set -eu; \
	tmpdir=$$(mktemp -d); \
	trap 'rm -rf "$$tmpdir"' EXIT; \
	mkdir -p "$$tmpdir/home/pirc/valet" "$$tmpdir/etc/systemd/system"; \
	cp deploy/valet-v2.service "$$tmpdir/etc/systemd/system/valet-v2.service"; \
	touch "$$tmpdir/home/pirc/valet/valet-v2"; \
	chmod +x "$$tmpdir/home/pirc/valet/valet-v2"; \
	for unit in sysinit.target basic.target network-online.target graphical.target; do \
		printf '[Unit]\nDescription=%s\n' "$$unit" > "$$tmpdir/etc/systemd/system/$$unit"; \
	done; \
	systemd-analyze verify --root="$$tmpdir" etc/systemd/system/valet-v2.service

lint:
	go run golang.org/x/tools/gopls/internal/analysis/modernize/cmd/modernize@latest -fix ./...
	golangci-lint run --fix
	.venv/bin/ruff check --fix .
	.venv/bin/ruff format .
	.venv/bin/ty check .
