# TUI Slash Command Autocomplete

## Overview

IronClaw's TUI now supports slash command autocomplete, similar to Claude Code. When you type `/` in the input area, a suggestion list appears below showing available commands. You can navigate with arrow keys, accept with Tab, or execute with Enter.

## Features

### 1. **Automatic Suggestion Display**
- Type `/` to trigger suggestions
- Suggestions appear in a styled box below the input area
- Shows up to 5 commands at a time with "... and N more" indicator

### 2. **Smart Filtering**
- **Exact match**: `/quit` → shows "quit" first
- **Prefix match**: `/q` → shows commands starting with "q"
- **Fuzzy match**: `/hel` → shows "help"
- **Alias support**: Commands can have multiple aliases

### 3. **Keyboard Navigation**
- **↑/↓ arrows**: Navigate through suggestions (wraps around)
- **Tab**: Accept selected suggestion without executing
- **Enter**: Accept and execute selected suggestion
- **Esc**: Dismiss suggestions

### 4. **Visual Feedback**
- Selected suggestion highlighted with purple background
- Command name and description shown for each suggestion
- Keyboard hints displayed at bottom of suggestion box

## Implementation Details

### Architecture

The implementation follows Claude Code's pattern with three main components:

#### 1. Command Registry (`commands.go`)
```go
type Command struct {
    Name        string
    Description string
    Aliases     []string
    ArgHint     string
    Category    string
}
```

Stores all available commands with metadata. Built-in commands:

**Session Management:**
- `/quit` (aliases: `exit`, `q`) - Exit the TUI
- `/clear` (alias: `cls`) - Clear conversation history
- `/reset` - Reset the current session

**Information:**
- `/help` (aliases: `h`, `?`) - Show available commands
- `/version` (alias: `v`) - Show IronClaw version
- `/status` - Show current session status

**Memory Management:**
- `/memory <list|search|clear>` - Memory management commands

**Skill Management:**
- `/skills` (alias: `skill`) - List available skills

**Export/History:**
- `/export [filename]` - Export conversation history
- `/history` (alias: `hist`) - Show conversation history

**Insights:**
- `/insights [days]` - Show evolution insights

#### 2. Suggestion System (`suggestions.go`)
```go
type SuggestionItem struct {
    Command     Command
    DisplayText string
}
```

Handles:
- Detecting "/" in input
- Filtering commands based on partial input
- Generating suggestion list with priority sorting
- Applying selected suggestion to input

#### 3. Model State (`model.go`)
```go
type Model struct {
    // ... existing fields ...
    suggestions         []SuggestionItem
    selectedSuggestion  int  // -1 means no selection
    showingSuggestions  bool
}
```

Manages:
- Suggestion state (list + selected index)
- Keyboard event handling for navigation
- Rendering suggestion box in View()

### Key Differences from Claude Code

1. **Simpler filtering**: Uses exact/prefix/fuzzy matching instead of Fuse.js
2. **No mid-input detection**: Only triggers on "/" at start of input
3. **No ghost text**: Doesn't show inline completion preview
4. **Circular navigation**: Arrow keys wrap around at list boundaries
5. **Auto-select first**: First suggestion auto-selected when list appears

### Styling

Suggestions use lipgloss styles defined in `styles.go`:
- `suggestionBoxStyle`: Rounded border with purple accent
- `selectedSuggestionStyle`: Bold white text on purple background
- `suggestionStyle`: Normal white text
- `suggestionHintStyle`: Dim italic text for keyboard hints

## Local vs LLM Commands

Commands are processed in two ways:

### Local Commands (No LLM)
These commands are handled directly by the TUI without calling the LLM:
- `/quit`, `/exit`, `/q` - Immediate exit
- `/clear`, `/cls` - Clear conversation locally
- `/help`, `/h`, `/?` - Display help text
- `/version`, `/v` - Show version info
- `/status` - Show session status
- `/history`, `/hist` - Show message history
- `/export [filename]` - Export conversation

### LLM Commands
Commands not handled locally are sent to the agent as regular messages:
- `/memory` - Requires LLM to manage memory
- `/skills` - Requires LLM to list and describe skills
- `/insights` - Requires LLM to analyze trajectory data
- `/reset` - Requires LLM to reset session state

## Usage Example

```
Type: /q
Shows:
┌─────────────────────────────────────────┐
│ Commands:                               │
│ ▶ quit                Exit the TUI      │
│                                         │
│ [↑↓] Navigate  [Tab] Accept  [Esc] Dismiss │
└─────────────────────────────────────────┘

Press ↓ to select next command
Press Tab to accept without executing
Press Enter to execute immediately
```

## Extending with Skills

To add skill commands to the autocomplete:

```go
import "github.com/Forest-Isle/IronClaw/internal/channel/tui"

// Register a skill command
tui.RegisterCommand(tui.Command{
    Name:        "analyze",
    Description: "Analyze code quality",
    Category:    "skill",
    ArgHint:     "<file>",
})
```

## Testing

Run tests with:
```bash
CGO_ENABLED=1 go test -tags fts5 ./internal/channel/tui/ -v
```

Tests cover:
- Command filtering (exact, prefix, fuzzy)
- Suggestion generation (with/without slash)
- Suggestion application (preserving arguments)

## Future Enhancements

Potential improvements:
1. **Mid-input detection**: Support `/` anywhere in input (like Claude Code)
2. **Fuzzy search library**: Use Fuse.js for better ranking
3. **Ghost text**: Show inline completion preview
4. **Command history**: Remember recently used commands
5. **Argument hints**: Show expected arguments for selected command
6. **Category grouping**: Group commands by category in display
