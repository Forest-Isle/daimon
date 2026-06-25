package tui

import (
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/mattn/go-runewidth"
)

var mdRenderer *glamour.TermRenderer
var rendererWidth int
var rendererMu sync.RWMutex

// newMarkdownRenderer builds a glamour renderer from the dark style with the
// document margin removed, so rendered agent lines sit flush against the "⏺"
// glyph (matching the plain streaming tail) instead of being indented 2 cols.
func newMarkdownRenderer(width int) *glamour.TermRenderer {
	cfg := styles.DarkStyleConfig
	noMargin := uint(0)
	cfg.Document.Margin = &noMargin // replace the pointer; don't mutate the shared default
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(cfg),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	return r
}

func init() {
	// Initialize with a default width, will be updated dynamically
	rendererWidth = 80
	mdRenderer = newMarkdownRenderer(rendererWidth)
}

// updateRendererWidth updates the markdown renderer width for proper text wrapping.
func updateRendererWidth(width int) {
	if width <= 0 {
		width = 80
	}
	// Reserve some space for padding and borders
	effectiveWidth := width - 4
	if effectiveWidth < 40 {
		effectiveWidth = 40
	}

	rendererMu.Lock()
	defer rendererMu.Unlock()

	if effectiveWidth != rendererWidth {
		rendererWidth = effectiveWidth
		mdRenderer = newMarkdownRenderer(rendererWidth)
	}
}

// renderMarkdown converts Markdown text to styled ANSI output.
// Falls back to plain text on error or if the renderer is unavailable.
func renderMarkdown(text string) string {
	rendererMu.RLock()
	r := mdRenderer
	w := rendererWidth
	rendererMu.RUnlock()

	if r == nil || text == "" {
		return wrapText(text, w)
	}
	rendered, err := r.Render(text)
	if err != nil {
		return wrapText(text, w)
	}
	return rendered
}

// wrappedRowCount returns how many display rows a single logical line occupies
// when soft-wrapped at the given display width. Used to size the input box.
// It counts on word boundaries, falling back to width-based breaks for tokens
// wider than the line, so it never under-counts the rows the textarea shows.
func wrappedRowCount(line string, width int) int {
	if width < 1 {
		return 1
	}
	if runewidth.StringWidth(line) <= width {
		return 1
	}
	rows := 1
	cur := 0
	for _, word := range strings.Fields(line) {
		ww := runewidth.StringWidth(word)
		if ww > width {
			// Long token breaks across multiple rows by width.
			if cur > 0 {
				rows++
				cur = 0
			}
			rows += (ww - 1) / width // additional full rows beyond the first
			cur = ww % width
			if cur == 0 {
				cur = width
			}
			continue
		}
		add := ww
		if cur > 0 {
			add++ // space separator
		}
		if cur+add > width {
			rows++
			cur = ww
		} else {
			cur += add
		}
	}
	return rows
}

// wrapText wraps plain text to the specified display width. Width is measured
// in terminal cells (runewidth), so CJK and other wide runes wrap correctly.
func wrapText(text string, width int) string {
	if width <= 0 {
		width = 80
	}

	var result strings.Builder
	lines := strings.Split(text, "\n")

	for i, line := range lines {
		if i > 0 {
			result.WriteString("\n")
		}

		// If line fits within width, keep it as is.
		if runewidth.StringWidth(line) <= width {
			result.WriteString(line)
			continue
		}

		// Wrap long lines on word boundaries.
		words := strings.Fields(line)
		if len(words) == 0 {
			result.WriteString(line)
			continue
		}

		currentLine := ""
		flush := func() {
			if currentLine != "" {
				result.WriteString(currentLine)
				result.WriteString("\n")
				currentLine = ""
			}
		}
		for _, word := range words {
			// A single CJK "word" has no spaces to break on; break it on rune
			// boundaries by display width. Latin words are left intact so long
			// identifiers/URLs are not split mid-token.
			if runewidth.StringWidth(word) > width && containsWideRune(word) {
				flush()
				chunks := breakByWidth(word, width)
				for i, chunk := range chunks {
					if i == len(chunks)-1 {
						currentLine = chunk // carry the remainder onto the line
					} else {
						result.WriteString(chunk)
						result.WriteString("\n")
					}
				}
				continue
			}

			testLine := currentLine
			if testLine != "" {
				testLine += " "
			}
			testLine += word

			if runewidth.StringWidth(testLine) > width {
				flush()
				currentLine = word
			} else {
				currentLine = testLine
			}
		}

		if currentLine != "" {
			result.WriteString(currentLine)
		}
	}

	return result.String()
}

// containsWideRune reports whether s has any double-width (e.g. CJK) rune.
func containsWideRune(s string) bool {
	for _, r := range s {
		if runewidth.RuneWidth(r) > 1 {
			return true
		}
	}
	return false
}

// breakByWidth splits s into chunks each at most width display columns,
// breaking on rune boundaries.
func breakByWidth(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	var chunks []string
	var cur strings.Builder
	curW := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if curW+rw > width {
			chunks = append(chunks, cur.String())
			cur.Reset()
			curW = 0
		}
		cur.WriteRune(r)
		curW += rw
	}
	if cur.Len() > 0 {
		chunks = append(chunks, cur.String())
	}
	return chunks
}
