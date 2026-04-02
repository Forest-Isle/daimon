## Requirements

### Requirement: Multi-layer progressive compression pipeline
The context compression system SHALL use a pipeline of 4 layers, each triggered at progressively higher context utilization thresholds. Each layer SHALL check if compression is still needed before executing.

#### Scenario: Low utilization — no compression
- **WHEN** estimated context utilization is below 30%
- **THEN** no compression layers SHALL execute

#### Scenario: Layer 1 — tool result eviction
- **WHEN** estimated context utilization exceeds `layers.tool_eviction_pct` (default 30%)
- **THEN** inline tool results larger than the persistence threshold SHALL be replaced with disk references (using the tool-result-persistence capability)

#### Scenario: Layer 2 — old turn summarization
- **WHEN** estimated context utilization exceeds `layers.summarize_pct` (default 50%) after layer 1
- **THEN** conversation turns older than half the history SHALL be summarized via an LLM call, preserving key facts and decisions

#### Scenario: Layer 3 — system prompt slimming
- **WHEN** estimated context utilization exceeds `layers.slim_prompt_pct` (default 70%) after layers 1-2
- **THEN** low-relevance memories and verbose skill metadata SHALL be removed from the system prompt

#### Scenario: Layer 4 — emergency truncation
- **WHEN** estimated context utilization exceeds `layers.emergency_pct` (default 90%) after layers 1-3
- **THEN** the oldest messages SHALL be dropped (keeping the last N turns) without an LLM call

### Requirement: Context utilization estimation
The compression pipeline SHALL estimate context utilization using a configurable character-to-token ratio (default: 4 characters per token) applied to the total size of system prompt + message history.

#### Scenario: Token estimation
- **WHEN** the total context size is 32000 characters and `token_estimate_ratio` is 0.25
- **THEN** the estimated token count SHALL be 8000

#### Scenario: Estimation against model limit
- **WHEN** the estimated token count is 8000 and the model's context window is 200000 tokens
- **THEN** the estimated utilization SHALL be 4%

### Requirement: Layer independence and early exit
Each compression layer SHALL be independently executable. The pipeline SHALL exit early if utilization drops below the next layer's threshold after any layer completes.

#### Scenario: Early exit after layer 1
- **WHEN** layer 1 (tool eviction) reduces utilization from 55% to 40%
- **THEN** layer 2 (summarization at 50%) SHALL NOT execute, avoiding an unnecessary LLM call

#### Scenario: Multiple layers needed
- **WHEN** utilization is at 80% and layer 1 reduces it to 60%
- **THEN** layer 2 SHALL execute next, and layer 3 SHALL be checked after layer 2

### Requirement: Legacy mode compatibility
The compression system SHALL support a `strategy: legacy` configuration that preserves the current single-pass LLM compression behavior for backward compatibility.

#### Scenario: Legacy mode
- **WHEN** `agent.compression.strategy` is set to `legacy`
- **THEN** the system SHALL use the existing `CompactHistory` function with its 40-message threshold

#### Scenario: Default mode
- **WHEN** `agent.compression.strategy` is not set or set to `layered`
- **THEN** the system SHALL use the multi-layer progressive pipeline

### Requirement: Compression pipeline configuration
All compression thresholds and parameters SHALL be configurable via the `agent.compression` section of the YAML config.

#### Scenario: Custom thresholds
- **WHEN** `layers.tool_eviction_pct` is set to 20 and `layers.summarize_pct` is set to 40
- **THEN** tool eviction SHALL trigger at 20% utilization and summarization at 40%
