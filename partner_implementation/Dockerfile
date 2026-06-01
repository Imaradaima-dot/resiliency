# ── Stage 1: Build ────────────────────────────────────────────────────────────
# Uses the official Go image as a builder.  Multi-stage build keeps the final
# image lean (~15MB) — NFR-02 from the requirements specification.
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Copy go.mod first (no go.sum needed — tidy generates it inside the container).
COPY go.mod go.sum ./

# Download and tidy in one layer so Docker caches it.
# go mod tidy generates go.sum, then downloads all dependencies.
RUN go mod tidy

# Copy all source files after deps are cached.
COPY . .

# Re-run tidy with full source present (resolves any indirect deps from code).
RUN go mod tidy

# Build a statically linked binary for the target service.
# Pass SERVICE as a build arg: ingestor | healthcheck | eda
ARG SERVICE=ingestor
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-w -s" \
    -o /service \
    ./cmd/${SERVICE}

# ── Stage 2: Run ──────────────────────────────────────────────────────────────
FROM alpine:3.19

# Install CA certificates so the service can reach MongoDB Atlas (TLS).
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Copy the compiled binary and a minimal data directory.
COPY --from=builder /service /app/service
RUN mkdir -p /app/data/raw /app/data/eda

# Health-check service listens on 8080 by default (NFR-05).
EXPOSE 8080

ENTRYPOINT ["/app/service"]
