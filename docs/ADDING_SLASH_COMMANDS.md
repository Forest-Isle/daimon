# Quick Reference: Adding New Slash Commands

## For Skill Developers

To add a new slash command for your skill:

```go
package main

import (
    "github.com/Forest-Isle/IronClaw/internal/channel/tui"
)

func init() {
    // Register your skill command
    tui.RegisterCommand(tui.Command{
        Name:        "analyze",
        Description: "Analyze code quality",
        Category:    "skill",
        ArgHint:     "<file>",
        Aliases:     []string{"check", "lint"},
    })
}
```

## For Core Developers

To add a built-in command, edit `internal/channel/tui/commands.go`:

```go
var commandRegistry = []Command{
    // ... existing commands ...
    {
        Name:        "debug",
        Description: "Toggle debug mode",
        Category:    "builtin",
    },
}
```

Then handle the command in `model.go` `handleChatKey()`:

```go
// /debug command
if text == "/debug" {
    m.addMessage("system", "🐛 Debug mode toggled.")
    m.viewport.SetContent(m.renderChat())
    m.viewport.GotoBottom()
    return m, nil
}
```

## Command Structure

```go
type Command struct {
    Name        string   // Command name (without /)
    Description string   // Short description shown in suggestions
    Aliases     []string // Alternative names
    ArgHint     string   // e.g., "<file>" or "[options]"
    Category    string   // "builtin", "skill", etc.
}
```

## Filtering Priority

Commands are matched in this order:
1. **Exact match**: `/quit` matches "quit" exactly
2. **Prefix match**: `/q` matches commands starting with "q"
3. **Alias match**: Checks all aliases for exact/prefix matches
4. **Fuzzy match**: `/hel` matches commands containing "hel"

## Testing Your Command

```bash
# Run TUI tests
CGO_ENABLED=1 go test -tags fts5 ./internal/channel/tui/ -v

# Build and test manually
CGO_ENABLED=1 go build -tags fts5 -o bin/ironclaw ./cmd/ironclaw
./bin/ironclaw tui
```

## Best Practices

1. **Keep names short**: Users type them frequently
2. **Add aliases**: Common abbreviations improve UX
3. **Clear descriptions**: Help users discover functionality
4. **Consistent categories**: Group related commands
5. **Argument hints**: Show expected parameters

## Example: Full Command Implementation

```go
// 1. Register the command
tui.RegisterCommand(tui.Command{
    Name:        "export",
    Description: "Export conversation to file",
    Category:    "builtin",
    ArgHint:     "<filename>",
    Aliases:     []string{"save"},
})

// 2. Handle in model.go
if strings.HasPrefix(text, "/export ") {
    filename := strings.TrimPrefix(text, "/export ")
    // ... export logic ...
    m.addMessage("system", fmt.Sprintf("💾 Exported to %s", filename))
    m.viewport.SetContent(m.renderChat())
    m.viewport.GotoBottom()
    return m, nil
}
```
