package mineru

import (
	"slices"
	"testing"
)

func TestOCRCommandArguments(t *testing.T) {
	args := commandArgs("input.pdf", "output", "pipeline")
	method := slices.Index(args, "-m")
	if method < 0 || method+1 >= len(args) || args[method+1] != "ocr" {
		t.Fatalf("MinerU method arguments = %v, want -m ocr", args)
	}
	if slices.Contains(args, "auto") {
		t.Fatalf("MinerU OCR arguments must not contain auto: %v", args)
	}
}

