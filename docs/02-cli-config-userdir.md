# 02. CLI, Config, and User Directory

## CLI Commands

The root command is `ironclaw`. The main command group is built in `cmd/ironclaw/main.go`.

| Command | Purpose |
|---|---|
| `ironclaw start -c <config>` | Start Gateway with configured non-TUI channels, currently including Telegram wiring from `runStart`. |
| `ironclaw tui -c <config>` | Start Gateway with the Bubble Tea TUI adapter. |
| `ironclaw version` | Print version, commit, and build date from linker flags. |
| `ironclaw skill list/search/install/update/remove` | Manage skills, using local skill loading and the external `clawhub` CLI for registry operations. |
| `ironclaw memory reindex` | Rebuild the file memory index from `~/.IronClaw/memory`. |
| `ironclaw agent run` | Subprocess backend entry point. Reads a JSON request from stdin and writes a JSON response to stdout. |
| `ironclaw mcp serve` | Start a standalone IronClaw MCP server over stdio or Streamable HTTP. Current standalone mode has minimal dependency wiring. |

## Config Load Order

```mermaid
flowchart TB
    Defaults[defaultConfig()] --> Explicit[Explicit YAML from -c]
    Explicit --> Env[Expand ${VAR}]
    Env --> Project[.ironclaw/ironclaw.yaml]
    Project --> Local[.ironclaw/local.yaml]
    Local --> Validate[validate()]
    Validate --> UserDir[userdir.Apply(~/.IronClaw)]
    UserDir --> Gateway[Gateway.New]
    Gateway --> Persisted[feature_state.json overrides]
```

`config.Load(path)` starts with defaults, reads the explicit YAML, expands `${VAR}`, warns about unknown top-level YAML keys, applies project/local overlays, and validates the result. Callers then run `userdir.Apply(cfg)` before constructing the Gateway.

`LoadHierarchy(workDir)` also exists for explicitly loading system, user, project, and local sources:

1. `/etc/ironclaw/config.yaml`
2. `~/.ironclaw/config.yaml`
3. `.ironclaw/ironclaw.yaml`
4. `.ironclaw/local.yaml`

The normal CLI path uses explicit config plus project/local overlays.

## Merge Semantics

`internal/config/merge.go` overlays non-zero scalar values. Slices such as extra dirs are appended. Permission rules use deny-first merge behavior through `MergePermissionRules`.

Important consequence: boolean fields that default to true are not always easy to turn off through generic overlay merging, because false is the zero value. Feature state and explicit feature override handling in Gateway should be considered when changing feature toggles.

## Major Config Groups

| Group | Key fields | Runtime use |
|---|---|---|
| `llm` | `provider`, `api_key`, `base_url`, `model`, `max_tokens`, `retry` | Selects Claude or OpenAI-compatible provider and retry wrapper. |
| `telegram`, `tui` | tokens, allowed users, auto approve, timeout | Channel adapter setup. |
| `agent` | `mode`, `max_iterations`, `system_prompt`, compression, speculative, team | Controls loop strategy, prompt, context compression, speculative execution, team workers. |
| `store` | `path` | SQLite database path. |
| `memory` | storage dir, embedding model/base URL/API key, fact extraction, lifecycle, cache, retention | File memory store, embeddings, fact extraction, reflection, compaction, retention. |
| `scheduler` | enabled, poll interval | Scheduled prompt execution. |
| `tools` | bash/file/http/verify/MCP/concurrency/result persistence | Built-in tool registration and execution behavior. |
| `permissions` | default action and ordered rules | PermissionEngine decisions. |
| `sandbox` | allowed/read-only dirs, bash backend, Docker config, network policy | File/network isolation and bash execution backend. |
| `observability` | enabled, exporter, endpoint, sample rate | OpenTelemetry and metrics setup. |

## User Directory

`userdir.Apply` manages `~/.IronClaw`:

```text
~/.IronClaw/
  Soul.md
  Memory.md
  Agent.md
  mcp/
    *.yaml
  skills/
  agents/
  memory/
```

Semantic roles:

- `Soul.md` -> `cfg.Agent.Personality`
- `Memory.md` -> `cfg.Agent.PersistentRules`
- `Agent.md` -> prepended to `cfg.Agent.SystemPrompt`

MCP files under `~/.IronClaw/mcp/*.yaml` are parsed into `cfg.Tools.MCP.Servers` unless a server with the same name already exists in project config. Skills and agent specs are loaded later by Gateway initializers.

## Feature Overrides

Feature defaults come from `internal/gateway/features.go`. Runtime changes can be persisted at `~/.IronClaw/feature_state.json`. Gateway applies persisted state after config overrides unless `GatewayOptions.SkipPersistedFeatureState` is set, which lets a caller boot with a clean, config-only feature set.

## Configuration Example

Minimal OpenAI-compatible local model setup:

```yaml
llm:
  provider: openai-compatible
  api_key: ""
  base_url: "http://localhost:11434/v1"
  model: llama3.1

memory:
  openai_api_key: "${OPENAI_API_KEY}"
  embedding_model: text-embedding-3-small
  embedding_base_url: "https://api.openai.com/v1"
```
