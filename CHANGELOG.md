# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.0] - 2026-04-11

### Added
- HTTP metrics endpoint (`/metrics`) on the optional HTTP gateway, exposing Prometheus-style counters for active sessions, total tool calls, LLM tokens used, and agent iteration counts; implemented in `internal/gateway/metrics.go`
- New config key `http.metrics_enabled` (default: `false`) to opt in to the metrics endpoint

## [0.1.2] - 2026-04-11

### Added
- Performance section in README documenting p50/p99 benchmarks for tool calls (10ms p99), LLM round trips (50ms p99), memory search, and knowledge base retrieval
- Troubleshooting section in README covering common issues: SQLite lock errors, FTS5 degradation, Telegram bot not responding, and LLM auth errors

### Changed
- Architecture diagram updated to highlight the RL Engine box (Bandit/PPO/DQN) more prominently

## [0.1.1] - 2026-04-11

### Fixed
- Memory consolidator no longer overwrites existing files when promoting session-scope memories to user scope — target path is now checked before the move, and a `_v2` suffix is appended if a file with the same name already exists, preventing silent data loss

## [0.1.0] - 2025-01-01

### Added

- Claude AI agent runtime with multi-turn conversation support
- Telegram Bot channel adapter with user-level access control
- Tool system: Bash execution, file I/O, HTTP requests, browser automation
- SQLite-based persistent storage
- Vector embedding memory search for long-term recall
- Cron-based task scheduler
- Configurable tool execution approval mechanism
- Optional HTTP gateway for REST API access
- Per-user session management with conversation history
- Context compaction for long conversations
- YAML-based configuration with environment variable support
