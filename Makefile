.PHONY: generate lint format check test build tidy clean help

# ─── Default ────────────────────────────────────────────────────────────────

help:
	@echo "Targets:"
	@echo "  generate   Regenerate Go code from .proto files (buf generate)"
	@echo "  lint       Lint .proto files (buf lint)"
	@echo "  format     Apply buf format -w to .proto files"
	@echo "  check      Verify .proto files are already formatted (buf format -d --exit-code)"
	@echo "  build      go build ./..."
	@echo "  test       go test -count=1 ./..."
	@echo "  tidy       go mod tidy"
	@echo "  clean      Remove generated .pb.go files"

# ─── Proto / buf ────────────────────────────────────────────────────────────

generate:
	buf generate

lint:
	buf lint

format:
	buf format -w

check:
	buf format -d --exit-code
	buf lint

# ─── Go ─────────────────────────────────────────────────────────────────────

build:
	go build ./...

test:
	go test -count=1 ./...

tidy:
	go mod tidy

# ─── Housekeeping ───────────────────────────────────────────────────────────

clean:
	find . -name '*.pb.go' -not -path './vendor/*' -delete
