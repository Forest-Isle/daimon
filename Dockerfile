# Build stage
FROM golang:1.23-bookworm AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=1 go build \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/ironclaw ./cmd/ironclaw

# Runtime stage
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

ENTRYPOINT ["/app/ironclaw"]
CMD ["start", "--config", "/app/configs/ironclaw.yaml"]
