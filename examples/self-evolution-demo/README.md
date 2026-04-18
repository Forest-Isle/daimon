# Self-Evolution Demo

This demo shows IronClaw's self-evolution loop in action: run tasks, generate insights, let the strategy optimizer tune parameters, re-run the same tasks, and measure the difference.

## Prerequisites

```bash
# Build IronClaw
make build
```

## Quick Start

Run the full demo with one command:

```bash
./demo.sh
```

This will:
1. Run the built-in evaluation suite (baseline)
2. Simulate trajectory data from the baseline
3. Trigger the insights cycle to generate strategy adjustments
4. Re-run the same evaluation suite (comparison)
5. Print a side-by-side comparison report

## Manual Steps

### Step 1: Run baseline evaluation

```bash
ironclaw eval run --suite builtin --output baseline.json
```

### Step 2: Check available tasks

```bash
ironclaw eval list
```

### Step 3: Generate insights from trajectory data

```bash
ironclaw insights report --days 7
```

### Step 4: View cognitive health metrics

```bash
ironclaw insights health --days 7
```

### Step 5: Run comparison evaluation (after evolution cycle)

```bash
ironclaw eval run --suite builtin --output after.json
```

### Step 6: Compare results

```bash
ironclaw eval compare --before baseline.json --after after.json
```

## What to Expect

The comparison report shows deltas in:
- **Success Rate** — percentage of tasks completed successfully
- **Assertion Pass Rate** — structured verification pass rate
- **Avg Confidence** — agent's self-assessed confidence
- **Avg Replan Count** — number of replans needed (lower is better)
- **Duration** — total execution time (lower is better)

A healthy evolution cycle should show:
- Higher or equal success rate
- Fewer replans (the agent learned better strategies)
- Stable or improved assertion pass rates

## Notes

- The `--suite builtin` uses deterministic tasks that don't require network access
- For real evolution data, enable `evolution.enabled: true` in your config and use IronClaw in cognitive mode for several sessions
- The `ironclaw insights health` command works offline from stored trajectory JSONL files
