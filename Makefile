# Generated artifacts (templ *_templ.go, web/static/css/app.css) are
# committed so `go build ./...` works with no tools installed; the
# targets below regenerate them and CI fails if they drift.

GO      ?= go
BIN     := bin/quorum
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

TOOLS := .tools

TAILWIND_VERSION := v4.3.3
UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)
ifeq ($(UNAME_S),Darwin)
	TAILWIND_OS := macos
else
	TAILWIND_OS := linux
endif
ifeq ($(UNAME_M),x86_64)
	TAILWIND_ARCH := x64
else
	TAILWIND_ARCH := arm64
endif
TAILWIND := $(TOOLS)/tailwindcss-$(TAILWIND_VERSION)

GOLANGCI_VERSION := v2.12.2
GOLANGCI := $(TOOLS)/golangci/$(GOLANGCI_VERSION)/golangci-lint

.PHONY: build run dev test lint generate css clean

build: generate css
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(BIN) ./cmd/quorum

run: build
	./$(BIN)

# Live-reload loop: tailwind rebuilds the CSS on change, templ rebuilds
# the templates and restarts the server on change.
dev: $(TAILWIND)
	@trap 'kill 0' EXIT INT TERM; \
	$(TAILWIND) -i web/styles/input.css -o web/static/css/app.css --watch=always & \
	QUORUM_LOG_FORMAT=text $(GO) tool templ generate --watch --cmd "$(GO) run ./cmd/quorum"

test: generate
	$(GO) test ./...

lint: $(GOLANGCI)
	$(GOLANGCI) run

generate:
	$(GO) tool templ generate
# sqlc arrives with M1:
	@if [ -f sqlc.yaml ]; then $(GO) tool sqlc generate; fi

css: $(TAILWIND)
	$(TAILWIND) -i web/styles/input.css -o web/static/css/app.css --minify

clean:
	rm -rf bin

$(TAILWIND):
	mkdir -p $(TOOLS)
	curl -fsSL -o $@ https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/tailwindcss-$(TAILWIND_OS)-$(TAILWIND_ARCH)
	chmod +x $@

$(GOLANGCI):
	mkdir -p $(dir $@)
	curl -fsSL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b $(dir $@) $(GOLANGCI_VERSION)
