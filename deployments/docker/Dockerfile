# Multi-stage build for IBM Cloud COS NFS Gateway

ARG ALPINE_VERSION=3.20

# Build stage
FROM golang:1.21-alpine3.20 AS builder

# Install build dependencies
RUN apk add --no-cache git make

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.Version=${VERSION:-dev}" \
    -o nfs-gateway \
    ./cmd/nfs-gateway

# Runtime stage
FROM alpine:${ALPINE_VERSION}

# Install runtime dependencies
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    && addgroup -g 1000 nfs \
    && adduser -D -u 1000 -G nfs nfs

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/nfs-gateway .

# Copy non-secret example configuration as the runtime default.
COPY --from=builder /build/configs/config.example.yaml /etc/nfs-gateway/config.yaml

# Create cache directory
RUN mkdir -p /var/cache/nfs-gateway && \
    chown -R nfs:nfs /var/cache/nfs-gateway /app

# Switch to non-root user
USER nfs

# Expose ports
EXPOSE 2049 8080 8081

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8081/health/live || exit 1

# Set entrypoint
ENTRYPOINT ["./nfs-gateway"]

# Default command
CMD ["--config", "/etc/nfs-gateway/config.yaml"]
