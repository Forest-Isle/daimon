# Discord Channel Adapter

## Overview

Adds a Discord bot adapter that implements all 5 channel interfaces (Channel, ApprovalSender, ReflectionSender, FeedbackSender, NotificationSender), enabling IronClaw to interact with users through Discord servers and direct messages.

## Architecture

### Interface Implementation

| Interface | Methods | Discord Mechanism |
|---|---|---|
| `Channel` | Name, Start, Send, SendStreaming, Stop | Discord Gateway + REST API |
| `ApprovalSender` | SendApprovalRequest | Message components (Approve/Deny/Always Approve buttons) |
| `ReflectionSender` | SendReflectionRequest | Message components (Continue/Adjust/Abort buttons) |
| `FeedbackSender` | SendFeedbackRequest | Message components (thumbs up/down buttons) |
| `NotificationSender` | SendNotification | Plain text message |

### Key Design Patterns

**Button-based interactions**: All approval/reflection/feedback flows use Discord message components (buttons) with custom IDs. Interaction responses are dispatched asynchronously to prevent Gateway deadlocks (same pattern used in the Telegram adapter's callback handling).

**User authorization**: Configurable allowlist of Discord user IDs. Empty list allows all users.

**Streaming**: Initial placeholder message sent, then edited incrementally. Rate-limited to 1 edit per second to respect Discord API limits. Same-content edits are skipped.

**Message limits**: Discord enforces a 2000-character limit. Messages are truncated with `...` suffix when exceeded.

**Auto-approve**: "Always Approve" button sets a session flag that auto-approves subsequent tool calls without prompting.

**Approval timeout**: Configurable (default 120s). On timeout, defaults to deny.

### Gateway Configuration

```go
sess.Identify.Intents = discordgo.IntentsGuildMessages | 
                         discordgo.IntentsDirectMessages | 
                         discordgo.IntentsMessageContent
```

Requires the **Message Content** privileged intent enabled in the Discord Developer Portal.

## Files

| File | Lines | Description |
|---|---|---|
| `internal/channel/discord/adapter.go` | 477 | Full adapter implementation |
| `internal/channel/discord/formatter.go` | 16 | Text formatting + truncation |
| `internal/channel/discord/adapter_test.go` | 129 | 8 tests for creation, streaming, formatting |

## Dependencies

- `github.com/bwmarrin/discordgo v0.29.0` (added to go.mod)

## Configuration

```yaml
discord:
  token: "${DISCORD_BOT_TOKEN}"
  allowed_user_ids: ["123456789", "987654321"]  # empty = allow all
```

## Bot Setup

1. Create a Discord application at https://discord.com/developers
2. Enable **Message Content** intent under Bot settings
3. Generate bot token and set as `DISCORD_BOT_TOKEN`
4. Invite bot to server with `bot` + `applications.commands` scopes
5. Start IronClaw with Discord channel enabled

## Testing

```bash
go test ./internal/channel/discord/...
```
