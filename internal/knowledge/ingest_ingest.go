package knowledge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Ingester parses a URI and returns extracted text content.
type Ingester interface {
	// CanHandle returns true if this ingester can handle the given sourceType.
	CanHandle(sourceType string) bool
	// Extract fetches and returns (title, content, error).
	Extract(ctx context.Context, uri string) (title string, content string, err error)
}

// Registry holds all registered ingesters.
type Registry struct {
	ingesters []Ingester
}

// NewRegistry creates a registry with default ingesters.
func NewRegistry() *Registry {
	r := &Registry{}
	r.Register(&MarkdownIngester{})
	r.Register(&CodeIngester{})
	r.Register(&WebIngester{})
	r.Register(&PDFIngester{})
	r.Register(&PlainTextIngester{})
	return r
}

// Register adds an ingester.
func (r *Registry) Register(i Ingester) {
	r.ingesters = append(r.ingesters, i)
}

// Extract finds the appropriate ingester and extracts content.
func (r *Registry) Extract(ctx context.Context, uri, sourceType string) (string, string, error) {
	for _, ing := range r.ingesters {
		if ing.CanHandle(sourceType) {
			return ing.Extract(ctx, uri)
		}
	}
	return "", "", fmt.Errorf("no ingester for source type %q", sourceType)
}

// DetectSourceType guesses the source type from URI.
func DetectSourceType(uri string) string {
	lower := strings.ToLower(uri)
	switch {
	case strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://"):
		return "web"
	case strings.HasSuffix(lower, ".pdf"):
		return "pdf"
	case strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".markdown"):
		return "markdown"
	case isCodeFile(lower):
		return "code"
	default:
		return "text"
	}
}

// ScanDir walks a directory and returns all file paths with their detected source types.
func ScanDir(dir string) ([]struct{ Path, SourceType string }, error) {
	var files []struct{ Path, SourceType string }
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			// Skip hidden and vendor directories
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		st := DetectSourceType(path)
		if st != "" {
			files = append(files, struct{ Path, SourceType string }{path, st})
		}
		return nil
	})
	return files, err
}

func isCodeFile(lower string) bool {
	codeExts := []string{
		".go", ".py", ".js", ".ts", ".java", ".c", ".cpp", ".h", ".rs",
		".rb", ".php", ".swift", ".kt", ".scala", ".sh", ".bash", ".zsh",
		".yaml", ".yml", ".json", ".toml", ".ini", ".conf",
	}
	for _, ext := range codeExts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}
