package tika

import (
	"strings"
	"testing"
)

func TestExtractTextRejectsPDFBeforeCallingTika(t *testing.T) {
	client := &Client{serverURL: "http://127.0.0.1:1"}

	_, err := client.ExtractText(strings.NewReader("%PDF-1.7"), "scanned.pdf")
	if err == nil || !strings.Contains(err.Error(), "PDF") || !strings.Contains(err.Error(), "MinerU") {
		t.Fatalf("expected PDF-to-MinerU routing error, got %v", err)
	}
}
