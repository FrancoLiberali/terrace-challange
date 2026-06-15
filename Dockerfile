# syntax=docker/dockerfile:1

# --- Builder ---
FROM golang:1.25-alpine AS builder

WORKDIR /src

# Dependency layer — go.mod / go.sum change less often than source, so
# this cache survives most edits.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 produces a static binary that runs on distroless
# without libc. -ldflags strips debug info / symbol table for size.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /out/arbd \
    ./cmd/arbd

# --- Runtime ---
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=builder /out/arbd /app/arbd
COPY config.yaml /app/config.yaml

ENTRYPOINT ["/app/arbd"]
