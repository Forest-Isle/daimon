# Training Data Export Pipeline — RLHF / DPO / SFT

## Overview

Adds a training data export pipeline that converts IronClaw's trajectory recordings into standard machine learning training formats (RLHF, DPO, SFT), enabling fine-tuning of custom models from agent experience.

## Supported Formats

| Format | Description | Use Case |
|--------|-------------|----------|
| **RLHF** | Reward-labeled `(prompt, response, reward)` tuples | Reward model training |
| **DPO** | Preference pairs `(prompt, chosen, rejected)` | Direct Preference Optimization |
| **SFT** | Successful `(instruction, output)` pairs | Supervised Fine-Tuning |

## Architecture

```
Trajectory JSONL Files (daily rotation)
    │
    ▼
readTrajectories(dir, since)
    │ filter by time, parse JSONL
    ▼
ExportTrainingData(cfg)
    │
    ├─ FormatRLHF → normalize rewards to [0,1], generate samples
    ├─ FormatDPO  → group by goal, pair best/worst as chosen/rejected
    └─ FormatSFT  → filter succeeded=true + confidence threshold
    │
    ▼
writeJSONL(outputPath)
```

### RLHF Export

Each trajectory becomes one sample:
```json
{
  "prompt": "Implement user authentication with JWT",
  "response": "bash(succeed, 250ms) → file_read(succeed, 100ms) → file_write(succeed, 300ms)",
  "reward": 0.85,
  "metadata": {"session_id": "sess_abc", "complexity": "moderate", "confidence": 0.92}
}
```

Rewards are normalized against the maximum reward in the dataset to produce [0, 1] range.

### DPO Export

Records are grouped by goal text. For each goal with 2+ trajectories, the highest-reward trajectory is `chosen` and the lowest is `rejected`:

```json
{
  "prompt": "Fix the login bug",
  "chosen": "bash(succeed, 200ms) → file_edit(succeed, 150ms)",
  "rejected": "bash(failed, 500ms) → bash(failed, 300ms) → bash(succeed, 800ms)",
  "metadata": {"chosen_reward": 12.5, "rejected_reward": 3.2}
}
```

Only produces pairs when there's a meaningful reward gap between best and worst.

### SFT Export

Filters to only `succeeded=true` records passing confidence and reward thresholds:

```json
{
  "instruction": "Create a REST API endpoint for user profiles",
  "output": "bash(succeed, 300ms) → file_write(succeed, 200ms) → http(succeed, 150ms)",
  "metadata": {"session_id": "sess_xyz", "confidence": 0.95}
}
```

## Usage

```go
result, err := eval.ExportTrainingData(eval.ExportConfig{
    TrajectoryDir: "~/.ironclaw/trajectory/",
    OutputDir:     "./training_data/",
    Format:        eval.FormatDPO,
    MinReward:     1.0,
    MinConfidence: 0.5,
    Since:         time.Now().AddDate(0, 0, -30), // last 30 days
})
// result.OutputPath = "./training_data/training_dpo.jsonl"
// result.Pairs = 47
```

## Files

| File | Lines | Description |
|---|---|---|
| `internal/eval/training_export.go` | 361 | Export pipeline with 3 format backends |
| `internal/eval/training_export_test.go` | 357 | 8 tests covering all formats + edge cases |

## Testing

```bash
go test -run TestExport ./internal/eval/...
```
