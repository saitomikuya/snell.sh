PNPM ?= pnpm
.PHONY: fmt test build web-build
fmt:
	gofmt -w cmd internal
test:
	go test ./...
	cd web && $(PNPM) test && $(PNPM) run typecheck
web-build:
	cd web && $(PNPM) run build
build: web-build
	go build -o bin/panel ./cmd/panel
