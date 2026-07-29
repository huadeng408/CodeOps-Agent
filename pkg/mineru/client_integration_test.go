package mineru

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRealMinerUOCR(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_MINERU_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_MINERU_E2E=1 to run the real MinerU integration")
	}

	pdfPath := filepath.Join(t.TempDir(), "image-only.pdf")
	generate := strings.Join([]string{
		"from PIL import Image, ImageDraw, ImageFont",
		"import sys",
		"image=Image.new('RGB',(1600,2200),'white')",
		"draw=ImageDraw.Draw(image)",
		"font=ImageFont.truetype(r'C:\\Windows\\Fonts\\arialbd.ttf',82)",
		"draw.text((120,300),'MINERU REAL OCR',fill='black',font=font)",
		"draw.text((120,520),'MINERU_REAL_OCR_20260729',fill='black',font=font)",
		"image.save(sys.argv[1],format='PDF',resolution=150.0)",
	}, ";")
	if output, err := exec.Command("python", "-c", generate, pdfPath).CombinedOutput(); err != nil {
		t.Fatalf("generate image-only PDF: %v: %s", err, output)
	}
	data, err := os.ReadFile(pdfPath)
	if err != nil {
		t.Fatal(err)
	}

	text, err := NewClient().ExtractText(context.Background(), data, filepath.Base(pdfPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ReplaceAll(text, `\_`, "_"), "MINERU_REAL_OCR_20260729") {
		t.Fatalf("OCR marker missing from MinerU output: %q", text)
	}
}
