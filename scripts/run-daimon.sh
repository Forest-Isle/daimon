#!/usr/bin/env bash
# Launch the resident Daimon agent with the local environment's gotchas handled.
#
# Why this script exists (learned from live full-link testing):
#   1. This network cannot reach api.telegram.org / api.openai.com directly —
#      route through the local proxy (Clash/Mihomo on 127.0.0.1:7897).
#   2. When launched from inside Claude Code, the shell inherits
#      ANTHROPIC_AUTH_TOKEN. The anthropic-sdk-go (used for the DeepSeek
#      Anthropic-compatible endpoint) prefers that Bearer token over the
#      config api key, so DeepSeek rejects it with 401. Unset it.
#   3. DEEPSEEK_API_KEY must be the valid key (exported by ~/.zshrc).
#
# Mail/SMTP creds live as literals in configs/daimon.yaml (gitignored).

set -euo pipefail
cd "$(dirname "$0")/.."

PROXY="${DAIMON_PROXY:-http://127.0.0.1:7897}"
export HTTP_PROXY="$PROXY" HTTPS_PROXY="$PROXY"
export NO_PROXY="localhost,127.0.0.1,0.0.0.0"

# The anthropic SDK must not pick up a leaked Claude Code session token.
unset ANTHROPIC_AUTH_TOKEN ANTHROPIC_API_KEY

if [ -z "${DEEPSEEK_API_KEY:-}" ]; then
  echo "DEEPSEEK_API_KEY is not set (expected from ~/.zshrc). Aborting." >&2
  exit 1
fi

exec ./bin/daimon start -c configs/daimon.yaml
