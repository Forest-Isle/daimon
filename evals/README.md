# Daimon Evals

A repeatable, mostly-deterministic eval harness for Daimon. Built per
[`error-analysis-v1.md`](./error-analysis-v1.md) (Phase-0 failure taxonomy) and
the agent-deepdive `phase-1-evals` course. Two surfaces:

1. **Triage / governance** — runs over the real replay corpus
   (`~/.daimon/replays/*.jsonl`), decomposes tool failures into
   governance-denied / agent-error / env / unknown (the FM-1 finding: most
   "failures" are governance denials, not agent mistakes), and renders a
   scorecard with a run-over-run Δ.
2. **Coding delegation** — grades a unified diff returned by a delegated coding
   agent against three deterministic acceptance gates (tests-green /
   no-test-tamper / in-scope). See `error-analysis-v1.md` §8.

## Run it

```bash
make eval            # scorecard over the replay corpus + Δ + coding-gate self-check
make eval-gate       # CI: non-zero exit if the coding-surface self-check fails
make eval-calibrate LABELS=evals/judge/calibration/testdata/labels.example.jsonl
```

`make eval` writes a baseline to `~/.daimon/.eval_score.json` only with
`-update`; subsequent runs show the Δ column against it.

## Layout

```
evals/
├── checks/        # deterministic checks (pure, no LLM)
│   ├── diff_accept.go   # 3-gate acceptance of a delegated coding diff
│   ├── diff_parse.go    # minimal unified-diff parser (stdlib only)
│   └── tool_failure.go  # classify tool-failure errors → {denied,agent,env,unknown}
├── runner/        # load real replay corpus, extract failures, aggregate
├── score/         # Scorecard, .last_score.json persistence, Δ render
├── judge/calibration/  # confusion matrix, TPR/TNR, Cohen's kappa, Wilson CI
├── cmd/eval/      # `make eval` entry
└── cmd/calibrate/ # `make eval-calibrate` entry
```

## Design notes

- **Deterministic core, no LLM in the hot path.** Everything `make eval` runs is
  pure Go, so it is fast, free, and reproducible. The LLM judge is the only
  ML box and is kept off the default path.
- **`-gate` gates only the zero-noise coding self-check.** Corpus counts grow as
  replay traffic accumulates, so hard-gating raw counts would fire on traffic
  growth, not regressions; the Δ column surfaces movement for human judgment
  instead.
- **Antihack is structural, not semantic.** The no-test-tamper gate catches
  deleted/renamed-away test files, removed declarations, added skips/build
  constraints, and net-removed assertions. Semantic weakening that preserves
  line structure (`if false {`, `want := got`) is left to the human merge
  sign-off and a future LLM-judge layer.

## Judge calibration

`evals/judge/calibration` answers "is the judge any good?" with numbers:
confusion matrix, TPR/TNR, raw agreement (Wilson CI), and **Cohen's kappa** —
the trust gate, because raw agreement inflates under class imbalance. The
example label set has 0.85 raw agreement but kappa 0.694.

The real judge is the existing `internal/replay` judge (`Rescore`) run offline;
fill `labels.jsonl` with `{"id","human","judge","split"}` where `human` is your
ground truth and `judge` is that judge's verdict, then `make eval-calibrate`.
The `BinaryJudge` interface + `ScoreItems` wire a judge into the loop; tests use
a deterministic stand-in so the math is verifiable without a provider.

## Status

Built and green (full race suite): FM-1 corpus decomposition, diff-acceptance
gate, scorecard + Δ, `make eval`/`eval-gate`/`eval-calibrate`, calibration
stats. The live corpus run reproduces the hand open-coding exactly (52 sessions,
39 failures, 33 governance-denied, memory×26).

Deferred (honest walls): the FM-3 salvage-vs-intent hybrid judge needs a
calibrated LLM judge (build labels first); golden coding-task runs through the
live agent need the autonomous-write question resolved (see §8). Real-traffic
saturation needs ≥100 traces across more than the email-triage corpus.
