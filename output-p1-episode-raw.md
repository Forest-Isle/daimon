Implemented Task P1-3 and wrote the required summary to [output-p1-episode.md](/Users/wuqisen/dev/IronClaw/output-p1-episode.md).

Added `internal/episode` with the runner, composer, reserved `episode_close` contract, salvage path, and tests. Added `world.Store.ApplyOutcome` plus journal idempotency coverage. I did not touch the existing agent loop/reflection/handle paths.

Verification passed:

- `GOCACHE=$PWD/.gocache make build-bin` exited 0, with a sandbox warning about global Go module stat-cache writes.
- `GOCACHE=$PWD/.gocache make vet`
- `GOCACHE=$PWD/.gocache make test-short`
- `GOCACHE=$PWD/.gocache CGO_ENABLED=1 go test -tags fts5 ./internal/episode/ ./internal/world/ ./internal/tool/`