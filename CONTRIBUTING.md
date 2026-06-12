# Contributing to IronClaw

This repository is a Go-first agent runtime. Keep changes small, source-derived, and verified.

## Local Setup

Required tools:

- Go 1.25.11 or compatible newer patch release.
- CGO-enabled toolchain for `github.com/mattn/go-sqlite3`.
- Git, because worktree tools and developer workflows depend on it.

```bash
cp configs/daimon.example.yaml configs/daimon.yaml
make build-bin
make test-short
```

## Worktree Workflow

For non-trivial changes, use an isolated worktree:

```bash
git worktree add .worktrees/<task-name> -b <branch-name>
cd .worktrees/<task-name>
```

Before merging, check all untracked and unstaged files:

```bash
git status --short
git diff main..HEAD
```


## Verification Matrix

Run the narrowest command that proves your change, then broaden when touching shared wiring:

```bash
make build-bin
make vet
make test-short
```

For Gateway, tool, memory, session, store, provider, or concurrency changes:

```bash
make test
```

For Studio changes:

```bash
npm ci
npm run build
```

## Code Style

- Match existing package patterns before adding new abstractions.
- Keep Gateway wiring explicit. It is the composition root, so hidden side effects make the system harder to audit.
- Prefer structured parsers and typed config over ad hoc string manipulation.
- Keep tool side effects behind the permission, hook, sandbox, verify, and audit chain.
- Add tests close to the module you change. Use broader integration tests when changing cross-package contracts.

## Pull Request Checklist

- The change has a focused purpose.
- `git status --short` has no accidental generated files.
- Relevant Go verification commands have passed.
- Config keys added in code are represented in `configs/daimon.example.yaml` or documented as internal.
- New tools declare capabilities and approval behavior.
- New Gateway features are registered in `internal/gateway/subsystem_feature.go` and defined in `internal/feature`.
