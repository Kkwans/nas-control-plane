GO ?= go

.PHONY: test vet build build-linux-arm64

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

build:
	mkdir -p bin
	$(GO) build -buildvcs=false -trimpath -o bin/ncp-agent ./cmd/ncp-agent
	$(GO) build -buildvcs=false -trimpath -o bin/ncp-server ./cmd/ncp-server

build-linux-arm64:
	mkdir -p bin
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -buildvcs=false -trimpath -o bin/ncp-agent-linux-arm64 ./cmd/ncp-agent
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -buildvcs=false -trimpath -o bin/ncp-server-linux-arm64 ./cmd/ncp-server
