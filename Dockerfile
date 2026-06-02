# Build stage
FROM golang:1.25-bookworm AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

RUN CGO_ENABLED=1 go build \
    -tags "fts5" \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o /out/ironclaw ./cmd/ironclaw

# Runtime stage — debian slim required for CGO (SQLite)
FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/*

RUN useradd -r -s /bin/false ironclaw

WORKDIR /app

COPY --from=builder /out/ironclaw /app/ironclaw
COPY configs/ironclaw.example.yaml /app/configs/ironclaw.example.yaml

RUN mkdir -p /app/data && chown -R ironclaw:ironclaw /app

USER ironclaw

EXPOSE 8080 9090 9191

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD /app/ironclaw version || exit 1

# OCI labels
LABEL org.opencontainers.image.title="IronClaw"
LABEL org.opencontainers.image.description="Local-first, self-evolving AI agent runtime"
LABEL org.opencontainers.image.version="${VERSION}"
LABEL org.opencontainers.image.revision="${COMMIT}"
LABEL org.opencontainers.image.created="${DATE}"

ENTRYPOINT ["/app/ironclaw"]
CMD ["start", "--config", "/app/configs/ironclaw.yaml"]
