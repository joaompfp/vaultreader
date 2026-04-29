# Stage 1: Build Go binary
FROM golang:1.21-alpine AS builder
WORKDIR /app
RUN apk add --no-cache git ca-certificates
COPY . .
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -buildvcs=false -ldflags="-s -w" -trimpath -o vaultreader .

# Stage 2: Minimal final image
FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/vaultreader /vaultreader
VOLUME ["/vaults", "/appdata"]
EXPOSE 8080
ENTRYPOINT ["/vaultreader"]
