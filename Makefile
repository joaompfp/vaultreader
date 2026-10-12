GO     ?= go
IMAGE  ?= ghcr.io/joaompfp/vaultreader
PLATFORMS ?= linux/amd64,linux/arm64
VERSION ?= dev
PORT   ?= 8080

.PHONY: test build image run-demo screenshots

# vet + Go tests + syntax check of the SPA's inline script
test:
	$(GO) vet ./...
	$(GO) test -count=1 ./...
	./scripts/check-inline-js.sh

build:
	CGO_ENABLED=0 $(GO) build -buildvcs=false -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o vaultreader ./cmd/vaultreader

# Multi-arch image build (no push; add --push to publish)
image:
	docker buildx build --platform $(PLATFORMS) --build-arg GIT_SHA=$(VERSION) -t $(IMAGE):$(VERSION) .

# Run the local binary against the bundled demo vault on :$(PORT).
# Appdata is a throwaway copy so examples/ stays clean.
run-demo: build
	@d=$$(mktemp -d) && cp -r examples/appdata/. $$d/ && \
	echo "VaultReader demo on http://localhost:$(PORT) (appdata: $$d)" && \
	./vaultreader -vaults examples/vaults -appdata $$d -port $(PORT)

# Regenerate README screenshots (needs Node + playwright-core; see script header)
screenshots: build
	node scripts/screenshots.mjs
