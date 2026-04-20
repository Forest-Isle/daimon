package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
)

var mdRenderer *glamour.TermRenderer
var rendererWidth int

func init() {
	// Initialize with a default width, will be updated dynamically
	rendererWidth = 80
	var err error
	mdRenderer, err = glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(rendererWidth),
	)
	if err != nil {
		mdRenderer = nil
	}
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

	if effectiveWidth != rendererWidth {
		rendererWidth = effectiveWidth
		var err error
		mdRenderer, err = glamour.NewTermRenderer(
			glamour.WithStandardStyle("dark"),
			glamour.WithWordWrap(rendererWidth),
		)
		if err != nil {
			mdRenderer = nil
		}
	}
}

// renderMarkdown converts Markdown text to styled ANSI output.
// Falls back to plain text on error or if the renderer is unavailable.
func renderMarkdown(text string) string {
	if mdRenderer == nil || text == "" {
		return wrapText(text, rendererWidth)
	}
	rendered, err := mdRenderer.Render(text)
	if err != nil {
		return wrapText(text, rendererWidth)
	}
	return rendered
}

// wrapText wraps plain text to the specified width.
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

		// If line is shorter than width, keep it as is
		if len(line) <= width {
			result.WriteString(line)
			continue
		}

		// Wrap long lines
		words := strings.Fields(line)
		if len(words) == 0 {
			result.WriteString(line)
			continue
		}

		currentLine := ""
		for _, word := range words {
			// If adding this word would exceed width, start a new line
			testLine := currentLine
			if testLine != "" {
				testLine += " "
			}
			testLine += word

			if len(testLine) > width {
				if currentLine != "" {
					result.WriteString(currentLine)
					result.WriteString("\n")
				}
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
