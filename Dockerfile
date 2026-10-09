# Multi-arch image (linux/amd64, linux/arm64). The Go stages run on the build
# host's own architecture and cross-compile, so no emulation is needed for
# them; only the final stage is per-target.
#
#   docker buildx build --platform linux/amd64,linux/arm64 .

# Stage 1: Build Go binary
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder
ARG TARGETARCH
WORKDIR /app
RUN apk add --no-cache git ca-certificates
COPY . .
RUN go mod tidy
# Fail the image build on any failing test (runs on the build host's arch).
RUN CGO_ENABLED=0 go test ./...
ARG GIT_SHA=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH \
    go build -buildvcs=false -ldflags="-s -w -X main.version=${GIT_SHA}" -trimpath -o vaultreader .

# Stage 2: officecli download, checked against pinned SHA-256 sums (taken from
# the release's SHA256SUMS). Bump version and both sums together.
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS officecli
ARG TARGETARCH
ARG OFFICECLI_VERSION=v1.0.90
ARG OFFICECLI_SHA256_AMD64=932d995b630916b097e4fd6acf6ea8158b2ccf02258803421829848d0c2ef036
ARG OFFICECLI_SHA256_ARM64=1b265d9bb2097aa8c73657f95574dda209de4e9ba90f11f3a416203ea2dbabdf
RUN apk add --no-cache curl ca-certificates && \
    case "$TARGETARCH" in \
      amd64) asset=officecli-linux-x64;   sum="$OFFICECLI_SHA256_AMD64" ;; \
      arm64) asset=officecli-linux-arm64; sum="$OFFICECLI_SHA256_ARM64" ;; \
      *) echo "unsupported TARGETARCH: $TARGETARCH" >&2; exit 1 ;; \
    esac && \
    curl -fsSLo /tmp/officecli \
      "https://github.com/iOfficeAI/OfficeCLI/releases/download/${OFFICECLI_VERSION}/${asset}" && \
    echo "${sum}  /tmp/officecli" | sha256sum -c - && \
    chmod 0755 /tmp/officecli

# Stage 3: Runtime — Microsoft's .NET runtime-deps image. Pre-baked with
# libicu, libstdc++, libgcc, ca-certificates, tzdata. No apt-install needed.
FROM mcr.microsoft.com/dotnet/runtime-deps:8.0-bookworm-slim
LABEL org.opencontainers.image.source="https://github.com/joaompfp/vaultreader" \
      org.opencontainers.image.licenses="AGPL-3.0-only" \
      org.opencontainers.image.title="VaultReader"
COPY --from=officecli --chmod=0755 /tmp/officecli /usr/local/bin/officecli
COPY --from=builder /app/vaultreader /vaultreader
VOLUME ["/vaults", "/appdata"]
EXPOSE 8080
ENTRYPOINT ["/vaultreader"]
