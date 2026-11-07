GO ?= go
VERSION ?= dev

.PHONY: build test check demo
build:
	$(GO) build -trimpath -ldflags "-X main.version=$(VERSION)" -o bin/gonote ./cmd/gonote

test:
	$(GO) test ./...

check:
	$(GO) vet ./...
	$(GO) test -race ./...

demo: build
	./bin/gonote --demo
