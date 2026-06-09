run:
	./scripts/setup-arp-scan-capabilities.sh
	set -a; . ./.env; set +a; go run .

test:
	go test -count=10 ./...

lint:
	golangci-lint run --fix
	.venv/bin/ruff check --fix .
	.venv/bin/ruff format .
	.venv/bin/ty check .
