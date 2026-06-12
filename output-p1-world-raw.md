Implemented the P1 world data-layer task and wrote the required summary to [output-p1-world.md](/Users/wuqisen/dev/IronClaw/output-p1-world.md).

Added:
- [027_world_model.sql](/Users/wuqisen/dev/IronClaw/internal/store/migrations/027_world_model.sql)
- [world.go](/Users/wuqisen/dev/IronClaw/internal/world/world.go)
- [world_test.go](/Users/wuqisen/dev/IronClaw/internal/world/world_test.go)

Verification passed:
- `make build-bin`
- `make vet`
- `make test-short`
- `go test -tags fts5 ./internal/world/ ./internal/store/`

`make build-bin` emitted a non-fatal Go module stat-cache permission warning, but exited `0`; I included that in the output summary.