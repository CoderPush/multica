//go:build parserintegration

package lark

import (
	"bytes"
	"context"
	"fmt"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This opt-in smoke test exercises installed Poppler/Tesseract with synthetic
// documents only. Default tests use fake executables and never need these tools.
func TestKnowledgeInstalledPDFParsers(t *testing.T) {
	if os.Getenv("MULTICA_RUN_PARSER_SMOKE") != "1" {
		t.Skip("set MULTICA_RUN_PARSER_SMOKE=1 for installed parser smoke")
	}
	const claim = "Synthetic resume: engineering leadership and AWS migration."
	content := []byte("BT /F1 20 Tf 40 700 Td (" + claim + ") Tj ET")
	textPDF := knowledgeTestPDF([][]byte{
		[]byte(`<< /Type /Catalog /Pages 2 0 R >>`),
		[]byte(`<< /Type /Pages /Kids [3 0 R] /Count 1 >>`),
		[]byte(`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 760 800] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>`),
		[]byte(`<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`),
		knowledgeTestPDFStream("", content),
	})
	ctx := context.Background()
	got, err := extractKnowledgeFile(ctx, "synthetic.pdf", textPDF)
	if err != nil || !strings.Contains(got, claim) {
		t.Fatalf("text PDF extraction failed: %q %v", got, err)
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "source.pdf")
	prefix := filepath.Join(dir, "scan")
	if err = os.WriteFile(input, textPDF, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = knowledgeCommand(ctx, "pdftoppm", "-scale-to", "1800", "-singlefile", "-jpeg", input, prefix); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(prefix + ".jpg")
	if err != nil {
		t.Fatal(err)
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	scanned := knowledgeTestPDF([][]byte{
		[]byte(`<< /Type /Catalog /Pages 2 0 R >>`),
		[]byte(`<< /Type /Pages /Kids [3 0 R] /Count 1 >>`),
		[]byte(`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 760 800] /Resources << /XObject << /Im0 4 0 R >> >> /Contents 5 0 R >>`),
		knowledgeTestPDFStream(fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode", config.Width, config.Height), data),
		knowledgeTestPDFStream("", []byte("q 760 0 0 800 0 0 cm /Im0 Do Q")),
	})
	got, err = extractKnowledgeFile(ctx, "synthetic-scan.pdf", scanned)
	if err != nil || !strings.Contains(got, "engineering leadership") || !strings.Contains(got, "AWS migration") {
		t.Fatalf("scanned PDF OCR failed: %q %v", got, err)
	}
}
func knowledgeTestPDFStream(dict string, data []byte) []byte {
	return append(append([]byte(fmt.Sprintf("<< %s /Length %d >>\nstream\n", dict, len(data))), data...), []byte("\nendstream")...)
}
func knowledgeTestPDF(objects [][]byte) []byte {
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, object := range objects {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n", i+1)
		out.Write(object)
		out.WriteString("\nendobj\n")
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return out.Bytes()
}
