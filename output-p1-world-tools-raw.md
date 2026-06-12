Implemented the world tools and gateway wiring, and wrote the required summary to [output-p1-world-tools.md](/Users/wuqisen/dev/IronClaw/output-p1-world-tools.md).

Verification passed:

- `make build-bin`
- `make vet`
- `make test-short`
- `CGO_ENABLED=1 go test -tags fts5 ./internal/tool/ ./internal/world/ ./internal/gateway/`

`make build-bin` emitted a Go stat-cache warning because the global module cache is outside the writable sandbox, but it exited 0.