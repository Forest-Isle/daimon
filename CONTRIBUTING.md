# Contributing to IronClaw

Thank you for your interest in contributing to IronClaw! This guide will help you get started.

## Getting Started

1. Fork the repository
2. Clone your fork:
   ```bash
   git clone https://github.com/YOUR_USERNAME/ironclaw.git
   cd ironclaw
   ```
3. Create a branch for your changes:
   ```bash
   git checkout -b feature/your-feature-name
   ```

## Development Setup

### Prerequisites

- Go 1.23 or later
- GCC (CGO is required for SQLite)
- golangci-lint (optional, for linting)

### Build & Test

```bash
# Build
make build

# Run tests
make test

# Lint
make lint

# Format code
make fmt
```

## Making Changes

### Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Keep functions focused and small
- Add comments for exported types and functions
- Write tests for new functionality

### Commit Messages

Use clear, descriptive commit messages:

```
feat: add Discord channel adapter
fix: handle empty response from Claude API
docs: update configuration examples
refactor: simplify session manager logic
```

Prefixes: `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `ci`

### Pull Requests

1. Ensure your code passes `make test` and `make lint`
2. Update documentation if your changes affect user-facing behavior
3. Keep PRs focused — one feature or fix per PR
4. Fill out the PR template completely

## Reporting Issues

- Use the [Bug Report](https://github.com/punkopunko/ironclaw/issues/new?template=bug_report.md) template for bugs
- Use the [Feature Request](https://github.com/punkopunko/ironclaw/issues/new?template=feature_request.md) template for new ideas
- Search existing issues before creating a new one

## Code of Conduct

This project follows the [Contributor Covenant Code of Conduct](CODE_OF_CONDUCT.md). By participating, you are expected to uphold this code.

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE).
