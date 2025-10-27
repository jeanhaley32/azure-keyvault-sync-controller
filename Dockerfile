# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /workspace

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY *.go ./

# Build the controller
# CGO_ENABLED=0 for static binary
# -ldflags="-w -s" to reduce binary size
# TARGETARCH is automatically set by buildx for multi-platform builds
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build \
    -ldflags="-w -s" \
    -o azure-keyvault-sync-controller \
    .

# Runtime stage - distroless for minimal attack surface
FROM gcr.io/distroless/static:nonroot

WORKDIR /

# Copy the binary from builder
COPY --from=builder /workspace/azure-keyvault-sync-controller .

# Use non-root user (distroless nonroot = UID 65532)
USER 65532:65532

ENTRYPOINT ["/azure-keyvault-sync-controller"]
