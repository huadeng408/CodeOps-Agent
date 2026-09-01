package pipeline

import (
	"fmt"
	"strings"

	"code-agent/internal/model"
)

// validatePDFStructuredChunk re-checks the PDF evidence contract at the Go
// trust boundary. The Python worker owns chunk construction, but its response
// must not be able to smuggle cross-page or Tika-derived evidence into the
// durable text index.
func validatePDFStructuredChunk(chunk model.StructuredChunk) error {
	if !strings.EqualFold(strings.TrimSpace(chunk.ParserName), "mineru") {
		return fmt.Errorf("PDF parser_name must be mineru, got %q", chunk.ParserName)
	}
	if len(chunk.PageSpan) != 2 || chunk.PageSpan[0] < 0 || chunk.PageSpan[1] < 0 {
		return fmt.Errorf("PDF page_span must contain two non-negative page numbers")
	}
	if chunk.PageSpan[0] != chunk.PageSpan[1] {
		return fmt.Errorf("PDF structured chunk must not span multiple pages: %v", chunk.PageSpan)
	}
	return nil
}
