# Stage 1: Build Go binary
FROM golang:1.24-alpine AS builder
WORKDIR /app
RUN apk add --no-cache git ca-certificates
COPY . .
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -buildvcs=false -ldflags="-s -w" -trimpath -o vaultreader .

# Stage 2: officecli download — uses the alpine builder image's apk so we
# don't need apt during the Docker build (which has DNS issues here).
FROM golang:1.24-alpine AS officecli
ARG OFFICECLI_VERSION=v1.0.90
RUN apk add --no-cache curl ca-certificates && \
    curl -fsSLo /tmp/officecli \
      "https://github.com/iOfficeAI/OfficeCLI/releases/download/${OFFICECLI_VERSION}/officecli-linux-x64" && \
    chmod 0755 /tmp/officecli

# Stage 3: Runtime — Microsoft's .NET runtime-deps image. Pre-baked with
# libicu, libstdc++, libgcc, ca-certificates, tzdata. No apt-install needed,
# bypasses the docker-build DNS limitation.
FROM mcr.microsoft.com/dotnet/runtime-deps:8.0-bookworm-slim
COPY --from=officecli --chmod=0755 /tmp/officecli /usr/local/bin/officecli
COPY --from=builder /app/vaultreader /vaultreader
VOLUME ["/vaults", "/appdata"]
EXPOSE 8080
ENTRYPOINT ["/vaultreader"]
