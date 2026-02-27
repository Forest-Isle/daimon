package ingest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// PlainTextIngester reads plain text files.
type PlainTextIngester struct{}

// CanHandle returns true for text source type.
func (p *PlainTextIngester) CanHandle(sourceType string) bool {
	return sourceType == "text"
}

// Extract reads a plain text file and returns its title and content.
func (p *PlainTextIngester) Extract(_ context.Context, uri string) (string, string, error) {
	data, err := os.ReadFile(uri)
	if err != nil {
		return "", "", err
	}
	base := filepath.Base(uri)
	title := strings.TrimSuffix(base, filepath.Ext(base))
	return title, string(data), nil
}
