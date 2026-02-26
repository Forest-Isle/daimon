# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in IronClaw, please report it responsibly.

**Do NOT open a public GitHub issue for security vulnerabilities.**

Instead, please send an email to **[INSERT SECURITY EMAIL]** with:

1. A description of the vulnerability
2. Steps to reproduce the issue
3. Potential impact assessment
4. Any suggested fixes (optional)

## Response Timeline

- **Acknowledgment**: Within 48 hours of receiving the report
- **Initial Assessment**: Within 7 days
- **Fix & Disclosure**: We aim to release a fix within 30 days of confirmation

## Scope

The following are in scope:

- IronClaw core runtime (`cmd/`, `internal/`)
- Tool execution security (bash, file, http)
- Configuration handling and secret management
- SQLite storage security

The following are out of scope:

- Third-party dependencies (please report to the respective maintainers)
- Issues in user-provided configuration or system prompts

## Supported Versions

| Version | Supported |
|---------|-----------|
| latest  | Yes       |

## Disclosure Policy

We follow coordinated disclosure. We will credit reporters in the release notes unless they prefer to remain anonymous.
