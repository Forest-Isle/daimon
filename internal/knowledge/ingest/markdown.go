package ingest

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// MarkdownIngester parses Markdown files and converts them to plain text.
type MarkdownIngester struct{}

// CanHandle returns true for markdown source type.
func (m *MarkdownIngester) CanHandle(sourceType string) bool {
	return sourceType == "markdown"
}

// Extract reads a Markdown file and returns its title and plain text content.
func (m *MarkdownIngester) Extract(_ context.Context, uri string) (string, string, error) {
	data, err := os.ReadFile(uri)
	if err != nil {
		return "", "", err
	}

	content := string(data)
	title := extractMarkdownTitle(content, filepath.Base(uri))
	cleaned := stripMarkdown(content)
	return title, cleaned, nil
}

// extractMarkdownTitle returns the first H1 heading, or falls back to filename.
func extractMarkdownTitle(content, fallback string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimPrefix(line, "# ")
		}
	}
	return strings.TrimSuffix(fallback, filepath.Ext(fallback))
}

var (
	headingRe    = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	codeBlockRe  = regexp.MustCompile("(?s)```[^`]*```")
	inlineCodeRe = regexp.MustCompile("`[^`]+`")
	linkRe       = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	imageRe      = regexp.MustCompile(`!\[[^\]]*\]\([^)]+\)`)
	boldRe       = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	italicRe     = regexp.MustCompile(`\*([^*]+)\*`)
	strikeRe     = regexp.MustCompile(`~~([^~]+)~~`)
	htmlTagRe    = regexp.MustCompile(`<[^>]+>`)
	multiNewline = regexp.MustCompile(`\n{3,}`)
)

// stripMarkdown converts Markdown to plain text suitable for indexing.
func stripMarkdown(md string) string {
	s := imageRe.ReplaceAllString(md, "")
	s = codeBlockRe.ReplaceAllStringFunc(s, func(block string) string {
		// Keep code block content but remove fences
		lines := strings.Split(block, "\n")
		if len(lines) > 2 {
			return strings.Join(lines[1:len(lines)-1], "\n")
		}
		return ""
	})
	s = inlineCodeRe.ReplaceAllStringFunc(s, func(m string) string {
		return strings.Trim(m, "`")
	})
	s = linkRe.ReplaceAllString(s, "$1")
	s = headingRe.ReplaceAllString(s, "")
	s = boldRe.ReplaceAllString(s, "$1")
	s = italicRe.ReplaceAllString(s, "$1")
	s = strikeRe.ReplaceAllString(s, "$1")
	s = htmlTagRe.ReplaceAllString(s, "")
	s = multiNewline.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
