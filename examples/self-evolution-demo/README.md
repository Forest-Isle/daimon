# Self-Evolution Demo

This example directory is reserved for demonstrating the self-evolution pipeline: trajectory collection, insights, preference learning, skill draft synthesis, and training export.

The current production code for these capabilities lives in:

- `internal/evolution`
- `internal/eval`
- `cmd/ironclaw/insights.go`
- `cmd/ironclaw/training.go`

Typical commands:

```bash
./bin/ironclaw insights report --days 7
./bin/ironclaw insights health --days 7
./bin/ironclaw training export --format reward --days 30
./bin/ironclaw training export --format dpo --days 30
./bin/ironclaw training export --format sft --days 30
```

Evolution is opt-in. Enable it in config when running live agent sessions that should produce trajectory and preference data:

```yaml
evolution:
  enabled: true
```

Generated trajectories and training exports may contain prompts, tool outputs, and private runtime context. Keep them out of version control unless deliberately creating sanitized fixtures.
