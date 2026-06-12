# Task P0-A: Rename IronClaw → Daimon

Context: this repo is being refounded as "Daimon" per `DAIMON_BLUEPRINT.md` (Phase 0, step 1).
You are on branch `refound/daimon`. The working tree contains unrelated uncommitted changes — do NOT revert, stage, commit, or checkout anything. No `git add/commit/checkout/restore` at all. Just edit files.

## Goal

Rename the project identity: Go module path, command directory, binary name, user data directory (with automatic migration), and build/release tooling. Behavior otherwise unchanged.

## Steps

1. **Module path**: `go.mod` module `github.com/Forest-Isle/IronClaw` → `github.com/Forest-Isle/daimon`. Rewrite all Go import paths accordingly (mechanical; `grep -rl 'Forest-Isle/IronClaw'` then sed, then gofmt).
2. **Command dir**: `git mv`-style rename is forbidden (no git) — use plain `mv cmd/ironclaw cmd/daimon`, fix references.
3. **Makefile**: `BINARY := daimon`, build paths `./cmd/daimon`.
4. **Build tooling**: update `.goreleaser.yml`, `Dockerfile`, `.github/workflows/*.yml` — binary name, cmd path, artifact names. Do not restructure these files; minimal token replacement.
5. **User dir** `~/.ironclaw` → `~/.daimon`:
   - Add ONE helper as the single source of truth, e.g. `userdir.BaseDir() string` (returns `~/.daimon`), and replace every scattered `".ironclaw"` literal in `internal/config/config.go`, `internal/config/validate.go`, `internal/config/config_infra.go`, `internal/userdir/userdir.go` (and any others `grep -rn '\.ironclaw' --include='*.go'` finds) with calls/derivations from it.
   - Migration on startup (call from the existing userdir init path): if `~/.daimon` does not exist AND `~/.ironclaw` exists and is a real dir (not symlink) → `os.Rename` it to `~/.daimon`, then create symlink `~/.ironclaw` → `~/.daimon` for compatibility; also inside the migrated dir, if `data/ironclaw.db` exists and `data/daimon.db` does not, rename it. Log what happened via slog. Idempotent; never destructive (if both dirs exist, leave everything alone and log a warning).
   - Default DB filename in config defaults: `daimon.db`.
6. **Example config**: `mv configs/ironclaw.example.yaml configs/daimon.example.yaml`, fix references to it in Go code and Makefile/docs lines that point at the path. Inside the file, update only self-referential names/paths.
7. **README.md / CLAUDE.md**: update only the binary/cmd/userdir path references (`cmd/ironclaw`, `ironclaw` binary, `~/.ironclaw`). Do NOT do a blanket prose rename of "IronClaw" across all docs — out of scope.
8. **Tests**: fix tests that assert on renamed strings/paths. Do not weaken assertions.

## Out of scope

- Any blueprint feature work (recorder, episodes, etc.).
- Renaming prose/branding in docs beyond step 7.
- Touching the unrelated uncommitted changes already in the tree.

## Verification (must pass, in order)

```bash
make build-bin
make vet
make test-short
make test        # CGO + fts5 + race; run if time permits, report result either way
grep -rn 'Forest-Isle/IronClaw' --include='*.go' . | wc -l   # must be 0
```

If the sandbox blocks Go's default build cache, use `GOCACHE=$PWD/.gocache` (and add `.gocache/` to `.gitignore`).

## Output

Write `output-p0-rename.md` at repo root: bullet list of files changed (grouped), migration logic summary, and the verbatim tail of each verification command's output (pass/fail).
