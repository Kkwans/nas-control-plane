GO ?= go

.PHONY: test vet build build-linux-arm64

NCP_VERSION ?= $(shell if test -f VERSION; then sed -n '1p' VERSION; else printf '%s' dev; fi)
NCP_LDFLAGS ?= -X github.com/Kkwans/nas-control-plane/internal/agentsocket.BuildVersion=$(NCP_VERSION)

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

build:
	mkdir -p bin
	$(GO) build -buildvcs=false -trimpath -ldflags "$(NCP_LDFLAGS)" -o bin/ncp-agent ./cmd/ncp-agent
	$(GO) build -buildvcs=false -trimpath -ldflags "$(NCP_LDFLAGS)" -o bin/ncp-server ./cmd/ncp-server

build-linux-arm64:
	mkdir -p bin
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -buildvcs=false -trimpath -ldflags "$(NCP_LDFLAGS)" -o bin/ncp-agent-linux-arm64 ./cmd/ncp-agent
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -buildvcs=false -trimpath -ldflags "$(NCP_LDFLAGS)" -o bin/ncp-server-linux-arm64 ./cmd/ncp-server
