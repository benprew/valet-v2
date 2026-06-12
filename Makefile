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
