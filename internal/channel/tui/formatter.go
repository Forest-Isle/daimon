package tui

import (
	"github.com/charmbracelet/glamour"
)

var mdRenderer *glamour.TermRenderer

func init() {
	var err error
	mdRenderer, err = glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(0), // let the viewport handle wrapping
	)
	if err != nil {
		mdRenderer = nil
	}
}

// renderMarkdown converts Markdown text to styled ANSI output.
// Falls back to plain text on error or if the renderer is unavailable.
func renderMarkdown(text string) string {
	if mdRenderer == nil || text == "" {
		return text
	}
	rendered, err := mdRenderer.Render(text)
	if err != nil {
		return text
	}
	return rendered
}
