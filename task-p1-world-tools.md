# Task P1-2: world tools + gateway wiring

Context: `DAIMON_BLUEPRINT.md` Phase 1 (§4.4 自编辑). `internal/world` data layer landed in Task P1-1 (`world.Store`, `world.Identity`, `world.Mutation`). This task exposes it to the model as tools and wires it into the gateway.

Branch `refound/daimon`; tree has unrelated uncommitted changes — no git mutations at all.

Deliberate deviation from blueprint (already decided, do not revisit): NO retrieval facade / embedding indexing in this task — prompt composition will use `Identity.Digest()` + `Store.CommitmentsDigest()` + the existing memory retrieval. Cross-layer vector indexing is deferred to the sleep/reconcile phase.

## Deliverables

### 1. Three tools in `internal/tool/` (mirror `internal/tool/memory.go` + `plan.go` patterns: schema, Execute, Capability, tests)

To avoid an import cycle (tool → world), check how existing tools take dependencies (e.g. `MemoryTool` holds `memory.Store`). Define narrow interfaces in the tool package if house style demands it (interface at use site), otherwise depend on `*world.Store` directly — match whichever pattern `internal/tool` already uses for `memory.Store`.

**`world_read`** (read-only; `ParallelSafety: safe`, not destructive):
- params: `section` enum: `identity` | `commitments` | `journal` (default `commitments`), optional `limit` (journal), optional `due_within` (commitments digest).
- identity → `Identity.Digest()`; commitments → `CommitmentsDigest`; journal → `ListJournal` formatted one-per-line.

**`commitment`** (write; not destructive):
- params: `action` enum `create` | `update` | `list`; for create: `kind`(project|promise|deadline|watch|routine), `title` (required), `body`, `due_at`, `horizon`; for update: `id` (required), optional `title/body/state/due_at/horizon` (state one of active|waiting|done|dropped); for list: optional `states` array.
- Maps onto `Store.CreateCommitment` / `UpdateCommitment` / `ListCommitments`. Validate enums; reject unknown states/kinds with clear error strings (model-facing).

**`world_edit`** (write; not destructive, PATH-FENCED):
- params: `file` (relative path like `preferences/coding.md` or `profile.md`), `content` (full replacement), optional `append` bool.
- Resolves strictly inside the identity dir: reject absolute paths, reject any path whose `filepath.Clean` escapes the identity root (`..`), reject symlink targets outside the root. Create parent dirs as needed. THIS FENCE IS SECURITY-CRITICAL — test it explicitly (`../x`, `/etc/x`, `a/../../x`).

### 2. Gateway wiring

- Construct `world.Store` from the existing SQLite handle and `world.Identity{Dir: <appdir>/world/identity}` (call `EnsureDir` at init); follow how `taskruntime` gets its DB handle (`internal/gateway/task_runtime.go`) and how plan/memory tools are registered (`internal/gateway/subsystem_tool.go`).
- Register all three tools unconditionally (like the `plan` tool — core capability, not feature-gated).
- Keep wiring local to the relevant `init_*.go` / subsystem file per CLAUDE.md guidance.

### 3. Tests

- Tool tests in `internal/tool/` (same package, table-driven): each action happy path + validation errors + the world_edit fence cases above. Use temp dir + in-memory/temp SQLite with migrations (mirror world_test.go bootstrap).
- One gateway-level registration assertion if an existing test pattern covers tool registration (check `tool_routing_test.go` / `subsystem_tool` tests; if none fits, skip — do not invent new harnesses).

## Out of scope

- episode package, Composer, retrieval facade, action-layer/hold/trust mechanics.
- Touching `taskruntime`, `memory`, `session`, `agent` loop files.

## Verification (must pass)

```bash
make build-bin
make vet
make test-short
CGO_ENABLED=1 go test -tags fts5 ./internal/tool/ ./internal/world/ ./internal/gateway/
```

Sandbox note: `GOCACHE=$PWD/.gocache` if needed.

## Output

Write `output-p1-world-tools.md` at repo root: files added/changed, tool schemas as the model sees them, fence test cases, verification tails, any deviations + reasons.
