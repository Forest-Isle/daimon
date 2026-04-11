# TUI Slash Command Autocomplete Demo

## Visual Flow

### 1. Initial State - Empty Input
```
┌─────────────────────────────────────────────────────────────┐
│  IronClaw v1.0.0  [cognitive]                               │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│ 15:04 You: Hello                                            │
│                                                             │
│ 15:04 Agent:                                                │
│ Hi! How can I help you today?                               │
│                                                             │
├─────────────────────────────────────────────────────────────┤
│ Type a message... (Enter to send, Ctrl+C to quit)          │
│ |                                                           │
└─────────────────────────────────────────────────────────────┘
```

### 2. User Types "/" - Suggestions Appear
```
┌─────────────────────────────────────────────────────────────┐
│  IronClaw v1.0.0  [cognitive]                               │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│ 15:04 You: Hello                                            │
│                                                             │
│ 15:04 Agent:                                                │
│ Hi! How can I help you today?                               │
│                                                             │
├─────────────────────────────────────────────────────────────┤
│ ╭─────────────────────────────────────────────────────────╮ │
│ │ Commands:                                               │ │
│ │ ▶ quit                Exit the TUI                      │ │
│ │   clear               Clear conversation history        │ │
│ │   help                Show available commands           │ │
│ │   reset               Reset the current session         │ │
│ │                                                         │ │
│ │   [↑↓] Navigate  [Tab] Accept  [Enter] Execute  [Esc] Dismiss │
│ ╰─────────────────────────────────────────────────────────╯ │
├─────────────────────────────────────────────────────────────┤
│ Type a message... (Enter to send, Ctrl+C to quit)          │
│ /|                                                          │
└─────────────────────────────────────────────────────────────┘
```

### 3. User Types "/q" - Filtered Results
```
┌─────────────────────────────────────────────────────────────┐
│  IronClaw v1.0.0  [cognitive]                               │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│ 15:04 You: Hello                                            │
│                                                             │
│ 15:04 Agent:                                                │
│ Hi! How can I help you today?                               │
│                                                             │
├─────────────────────────────────────────────────────────────┤
│ ╭─────────────────────────────────────────────────────────╮ │
│ │ Commands:                                               │ │
│ │ ▶ quit                Exit the TUI                      │ │
│ │                                                         │ │
│ │   [↑↓] Navigate  [Tab] Accept  [Enter] Execute  [Esc] Dismiss │
│ ╰─────────────────────────────────────────────────────────╯ │
├─────────────────────────────────────────────────────────────┤
│ Type a message... (Enter to send, Ctrl+C to quit)          │
│ /q|                                                         │
└─────────────────────────────────────────────────────────────┘
```

### 4. User Presses Down Arrow - Selection Moves
```
┌─────────────────────────────────────────────────────────────┐
│  IronClaw v1.0.0  [cognitive]                               │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│ 15:04 You: Hello                                            │
│                                                             │
│ 15:04 Agent:                                                │
│ Hi! How can I help you today?                               │
│                                                             │
├─────────────────────────────────────────────────────────────┤
│ ╭─────────────────────────────────────────────────────────╮ │
│ │ Commands:                                               │ │
│ │   quit                Exit the TUI                      │ │
│ │ ▶ clear               Clear conversation history        │ │
│ │   help                Show available commands           │ │
│ │   reset               Reset the current session         │ │
│ │                                                         │ │
│ │   [↑↓] Navigate  [Tab] Accept  [Enter] Execute  [Esc] Dismiss │
│ ╰─────────────────────────────────────────────────────────╯ │
├─────────────────────────────────────────────────────────────┤
│ Type a message... (Enter to send, Ctrl+C to quit)          │
│ /|                                                          │
└─────────────────────────────────────────────────────────────┘
```

### 5. User Presses Tab - Command Accepted
```
┌─────────────────────────────────────────────────────────────┐
│  IronClaw v1.0.0  [cognitive]                               │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│ 15:04 You: Hello                                            │
│                                                             │
│ 15:04 Agent:                                                │
│ Hi! How can I help you today?                               │
│                                                             │
├─────────────────────────────────────────────────────────────┤
│ Type a message... (Enter to send, Ctrl+C to quit)          │
│ /clear |                                                    │
└─────────────────────────────────────────────────────────────┘
```

### 6. User Presses Enter - Command Executed
```
┌─────────────────────────────────────────────────────────────┐
│  IronClaw v1.0.0  [cognitive]                               │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│ 15:05 🔄 Conversation cleared.                              │
│                                                             │
├─────────────────────────────────────────────────────────────┤
│ Type a message... (Enter to send, Ctrl+C to quit)          │
│ |                                                           │
└─────────────────────────────────────────────────────────────┘
```

## Keyboard Shortcuts Summary

| Key | Action |
|-----|--------|
| `/` | Trigger suggestions |
| `↑` | Navigate up (wraps to bottom) |
| `↓` | Navigate down (wraps to top) |
| `Tab` | Accept selected suggestion without executing |
| `Enter` | Accept and execute selected suggestion |
| `Esc` | Dismiss suggestions |

## Color Scheme

- **Selected suggestion**: Bold white text on purple background (`#7D56F4`)
- **Unselected suggestions**: Normal white text
- **Border**: Purple accent (`#7D56F4`)
- **Hints**: Dim gray italic text (`#626262`)

## Implementation Notes

1. **Auto-selection**: First suggestion is automatically selected when list appears
2. **Circular navigation**: Arrow keys wrap around at list boundaries
3. **Real-time filtering**: Suggestions update as you type
4. **Preserved arguments**: Tab completion preserves any arguments after the command
5. **Max display**: Shows up to 5 suggestions with "... and N more" indicator
