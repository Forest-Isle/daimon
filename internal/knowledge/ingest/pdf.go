package ingest

import (
	"context"
	"fmt"
)

// PDFIngester is a stub for PDF parsing.
// Full implementation requires pdfcpu or similar; add in a future phase.
type PDFIngester struct{}

// CanHandle returns true for pdf source type.
func (p *PDFIngester) CanHandle(sourceType string) bool {
	return sourceType == "pdf"
}

// Extract returns an error indicating PDF is not yet supported.
func (p *PDFIngester) Extract(_ context.Context, uri string) (string, string, error) {
	return "", "", fmt.Errorf("PDF ingestion not supported yet (uri=%s); convert to text first", uri)
}
