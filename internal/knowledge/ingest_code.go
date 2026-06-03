package knowledge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CodeIngester reads source code files and wraps them with metadata comments.
type CodeIngester struct{}

// CanHandle returns true for code source type.
func (c *CodeIngester) CanHandle(sourceType string) bool {
	return sourceType == "code"
}

// Extract reads a source code file and returns its title and annotated content.
func (c *CodeIngester) Extract(_ context.Context, uri string) (string, string, error) {
	data, err := os.ReadFile(uri)
	if err != nil {
		return "", "", err
	}

	title := filepath.Base(uri)
	ext := strings.TrimPrefix(filepath.Ext(uri), ".")
	content := fmt.Sprintf("// File: %s\n// Language: %s\n\n%s", uri, ext, string(data))
	return title, content, nil
}
