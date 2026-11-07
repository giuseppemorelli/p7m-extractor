BINARY    := p7m-extractor
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 windows/amd64 windows/arm64 darwin/amd64 darwin/arm64
GOROOT    := $(shell go env GOROOT)
# wasm_exec.js deve corrispondere alla versione di Go usata per compilare.
WASM_EXEC := $(firstword $(wildcard $(GOROOT)/lib/wasm/wasm_exec.js $(GOROOT)/misc/wasm/wasm_exec.js))

.PHONY: build release web test clean

# Compila per il sistema corrente in bin/
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .

# Compila per tutte le piattaforme in dist/
release:
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=""; \
		[ "$$os" = "windows" ] && ext=".exe"; \
		out="dist/$(BINARY)-$$os-$$arch$$ext"; \
		echo "  $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o "$$out" . || exit 1; \
	done

# Sito statico con WebAssembly (es. Cloudflare Pages) in dist/web/
web:
	@test -n "$(WASM_EXEC)" || { echo "wasm_exec.js non trovato in $(GOROOT)"; exit 1; }
	@mkdir -p dist/web
	GOOS=js GOARCH=wasm go build -trimpath -ldflags "-s -w" -o dist/web/p7m.wasm ./cmd/wasm
	cp "$(WASM_EXEC)" dist/web/wasm_exec.js
	cp web/index.html web/_headers dist/web/
	@ls -l dist/web

test:
	go vet ./...
	GOOS=js GOARCH=wasm go vet ./cmd/wasm ./p7m
	go test ./...

clean:
	rm -rf bin dist
