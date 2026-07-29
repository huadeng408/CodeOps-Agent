package documentparser

import (
	"context"
	"io"
	"strings"
	"testing"
)

type fakeTika struct {
	calls int
}

func (f *fakeTika) ExtractText(_ io.Reader, _ string) (string, error) {
	f.calls++
	return "tika text", nil
}

type fakeMinerU struct {
	calls int
}

func (f *fakeMinerU) ExtractText(_ context.Context, _ []byte, _ string) (string, error) {
	f.calls++
	return "mineru ocr", nil
}

func TestPDFUsesMinerUAndNeverTika(t *testing.T) {
	tika := &fakeTika{}
	minerU := &fakeMinerU{}
	client := New(tika, minerU)

	text, err := client.ExtractText(context.Background(), strings.NewReader("%PDF-1.7"), "scan.PDF")
	if err != nil {
		t.Fatal(err)
	}
	if text != "mineru ocr" || minerU.calls != 1 || tika.calls != 0 {
		t.Fatalf("unexpected routing: text=%q mineru=%d tika=%d", text, minerU.calls, tika.calls)
	}
}

func TestNonPDFUsesTikaAndNeverMinerU(t *testing.T) {
	tika := &fakeTika{}
	minerU := &fakeMinerU{}
	client := New(tika, minerU)

	text, err := client.ExtractText(context.Background(), strings.NewReader("docx"), "notes.docx")
	if err != nil {
		t.Fatal(err)
	}
	if text != "tika text" || tika.calls != 1 || minerU.calls != 0 {
		t.Fatalf("unexpected routing: text=%q mineru=%d tika=%d", text, minerU.calls, tika.calls)
	}
}

func TestPDFRejectsInvalidSignatureBeforeMinerU(t *testing.T) {
	client := New(&fakeTika{}, &fakeMinerU{})

	_, err := client.ExtractText(context.Background(), strings.NewReader("not-pdf"), "scan.pdf")
	if err == nil || !strings.Contains(err.Error(), "invalid PDF") {
		t.Fatalf("expected invalid PDF error, got %v", err)
	}
}

func TestRenamedPDFUsesMinerUMagicAndNeverTika(t *testing.T) {
	tika := &fakeTika{}
	minerU := &fakeMinerU{}
	client := New(tika, minerU)

	_, err := client.ExtractText(context.Background(), strings.NewReader("%PDF-1.7"), "scan.bin")
	if err != nil {
		t.Fatal(err)
	}
	if minerU.calls != 1 || tika.calls != 0 {
		t.Fatalf("unexpected routing: mineru=%d tika=%d", minerU.calls, tika.calls)
	}
}
