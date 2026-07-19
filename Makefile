GO ?= go

.PHONY: test vet build build-linux-arm64

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

build:
	mkdir -p bin
	$(GO) build -trimpath -o bin/ncp-agent ./cmd/ncp-agent

build-linux-arm64:
	mkdir -p bin
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -trimpath -o bin/ncp-agent-linux-arm64 ./cmd/ncp-agent
