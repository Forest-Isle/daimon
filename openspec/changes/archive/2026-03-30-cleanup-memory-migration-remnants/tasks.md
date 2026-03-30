## 1. Fix Compilation Errors (P0)

- [x] 1.1 Remove extra closing brace `}` at `consolidator.go:194`
- [x] 1.2 Update `gateway.go:134` — change `memory.NewFileStore(...)` to `memory.NewFileMemoryStore(...)` with correct parameter signature
- [x] 1.3 Remove `gateway.go:143-148` — delete `SQLiteStore` fallback branch (`storageType != "file"` path) and `*memory.SQLiteStore` type assertion at line 160
- [x] 1.4 Update `cmd/ironclaw/memory.go:85,172,234` — change `memory.NewFileStore(...)` to `memory.NewFileMemoryStore(...)` with correct parameters
- [x] 1.5 Verify project compiles: `CGO_ENABLED=1 go build -tags fts5 ./...`

## 2. Delete Dead Code Files

- [x] 2.1 Delete `internal/memory/index.go` — JSON IndexManager, no external references
- [x] 2.2 Delete `internal/memory/metadata.go` — ChunkMetadata/MetadataManager, no external references
- [x] 2.3 Delete `internal/memory/txlog.go` — TransactionLog, no external references
- [x] 2.4 Delete `internal/memory/chunker.go` — Chunker, no external references
- [x] 2.5 Delete `internal/memory/vss.go` — VSSIndexer, all queries reference defunct `memory_facts_vss` table; vector search already implemented in `file_store.go`
- [x] 2.6 Verify no compilation errors after deletion

## 3. Fix ForgettingCurveManager Dependencies

- [x] 3.1 Change `forgetting_curve.go` struct field from `store *SQLiteStore` to `db *store.DB`
- [x] 3.2 Update `NewForgettingCurveManager` constructor parameter accordingly
- [x] 3.3 Rewrite `FadeWeakMemories()` to query `memory_index` table instead of `memory_facts` (SELECT file_path, strength WHERE strength < 0.3)
- [x] 3.4 Implement file archival in `FadeWeakMemories()`: move matched files to `archived/` subdirectory and update `memory_index`
- [x] 3.5 Update `gateway.go` to pass correct dependencies when constructing `ForgettingCurveManager`
- [x] 3.6 Update `forgetting_curve_test.go` to use new constructor and `memory_index` table

## 4. Simplify Store Interface

- [x] 4.1 Merge `SaveFact` into `Save` in `store.go` — remove `SaveFact` method from `Store` interface
- [x] 4.2 Merge `DeleteFact` into `Delete` in `store.go` — remove `DeleteFact` method
- [x] 4.3 Rename `UpdateFact` to `Update` in `store.go`
- [x] 4.4 Update `FileMemoryStore` implementation to match new interface method names
- [x] 4.5 Update all callers in `lifecycle.go`, `gateway.go`, `facts.go` to use new method names
- [x] 4.6 Clean up outdated comments referencing `memory_facts` table in `store.go` and `lifecycle.go`

## 5. Update Tests

- [x] 5.1 Fix `benchmark_test.go` — remove `NewSQLiteStore` reference, rewrite to use `NewFileMemoryStore`
- [x] 5.2 Fix `forgetting_curve_test.go` — already covered in 3.6 but verify assertions match new table structure
- [x] 5.3 Run full test suite: `CGO_ENABLED=1 go test -tags fts5 ./internal/memory/ -v`
- [x] 5.4 Run project-wide tests: `CGO_ENABLED=1 go test -tags fts5 ./...`

## 6. Final Verification

- [x] 6.1 Run `go vet ./...` and `golangci-lint run` to catch any remaining issues
- [x] 6.2 Verify no non-migrator references to `memory_facts` remain: `grep -r "memory_facts" internal/ --include="*.go" | grep -v migrator | grep -v _test | grep -v migration`
- [x] 6.3 Verify file count in `internal/memory/` reduced from 25 to ~20
