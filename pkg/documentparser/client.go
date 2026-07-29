// Package documentparser routes PDF extraction to MinerU and other documents to Tika.
package documentparser

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// TikaExtractor extracts text from non-PDF office documents.
type TikaExtractor interface {
	ExtractText(io.Reader, string) (string, error)
}

// MinerUExtractor extracts OCR text from PDF documents.
type MinerUExtractor interface {
	ExtractText(context.Context, []byte, string) (string, error)
}

// Client enforces the parser boundary for supported documents.
type Client struct {
	tika   TikaExtractor
	minerU MinerUExtractor
}

// New creates a document parser router.
func New(tika TikaExtractor, minerU MinerUExtractor) *Client {
	return &Client{tika: tika, minerU: minerU}
}

// ExtractText routes PDFs exclusively to MinerU and all other files to Tika.
func (c *Client) ExtractText(ctx context.Context, reader io.Reader, fileName string) (string, error) {
	buffered := bufio.NewReader(reader)
	header, _ := buffered.Peek(len("%PDF-"))
	isPDF := strings.EqualFold(filepath.Ext(fileName), ".pdf") || string(header) == "%PDF-"
	if isPDF {
		data, err := io.ReadAll(buffered)
		if err != nil {
			return "", fmt.Errorf("read PDF file %s: %w", fileName, err)
		}
		if !bytes.HasPrefix(data, []byte("%PDF-")) {
			return "", fmt.Errorf("invalid PDF file: %s", fileName)
		}
		return c.minerU.ExtractText(ctx, data, fileName)
	}
	return c.tika.ExtractText(buffered, fileName)
}
