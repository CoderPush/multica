package lark

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const knowledgeMaxFileBytes = 20 << 20
const knowledgeMaxTextBytes = 256 << 10
const knowledgeMaxPDFPages = 60
const knowledgeMaxOCRPages = 20

func extractKnowledgeFile(ctx context.Context, name string, data []byte) (string, error) {
	if len(data) > knowledgeMaxFileBytes {
		return "", permanentKnowledgeError("file exceeds 20 MiB; provide a smaller source")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	switch strings.ToLower(filepath.Ext(name)) {
	case ".docx":
		return extractKnowledgeDOCX(ctx, data)
	case ".pdf":
		return extractKnowledgePDF(ctx, data)
	default:
		return "", permanentKnowledgeError("unsupported file format; only PDF and DOCX are indexed")
	}
}
func extractKnowledgeDOCX(ctx context.Context, data []byte) (string, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", permanentKnowledgeError("corrupt DOCX; original retained")
	}
	if len(archive.File) > 2048 {
		return "", permanentKnowledgeError("DOCX archive entry limit exceeded")
	}
	var document *zip.File
	var total uint64
	for _, f := range archive.File {
		if f.UncompressedSize64 > 40<<20 {
			return "", permanentKnowledgeError("DOCX expansion limit exceeded")
		}
		total += f.UncompressedSize64
		if total > 40<<20 {
			return "", permanentKnowledgeError("DOCX expansion limit exceeded")
		}
		if f.Name == "word/document.xml" {
			if document != nil {
				return "", permanentKnowledgeError("ambiguous DOCX document entry")
			}
			document = f
		}
	}
	if document == nil || document.UncompressedSize64 > 5<<20 {
		return "", permanentKnowledgeError("DOCX document missing or exceeds text limit")
	}
	r, err := document.Open()
	if err != nil {
		return "", permanentKnowledgeError("corrupt DOCX; original retained")
	}
	defer r.Close()
	raw, err := io.ReadAll(io.LimitReader(r, 5<<20+1))
	if err != nil || len(raw) > 5<<20 {
		return "", permanentKnowledgeError("DOCX document exceeds text limit or is corrupt")
	}
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	var out strings.Builder
	paragraph := 0
	inText := false
	readable := 0
	const wordNS = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		tok, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", permanentKnowledgeError("invalid DOCX XML; original retained")
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			if tok.Name.Space != wordNS {
				continue
			}
			if tok.Name.Local == "p" {
				paragraph++
				fmt.Fprintf(&out, "\n[paragraph %d] ", paragraph)
			}
			if tok.Name.Local == "t" {
				inText = true
			}
			if tok.Name.Local == "tab" || tok.Name.Local == "br" {
				out.WriteByte(' ')
			}
		case xml.EndElement:
			if tok.Name.Space == wordNS && tok.Name.Local == "t" {
				inText = false
			}
		case xml.CharData:
			if inText {
				out.Write(tok)
				readable += len(strings.TrimSpace(string(tok)))
			}
		}
		if out.Len() > knowledgeMaxTextBytes {
			return "", permanentKnowledgeError("extracted text exceeds 256 KiB; original retained")
		}
	}
	if readable == 0 {
		return "", permanentKnowledgeError("DOCX contains no readable text; original retained")
	}
	return out.String(), nil
}

type knowledgeBoundedOutput struct {
	bytes.Buffer
	limit int
}

func (b *knowledgeBoundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("parser output limit exceeded")
	}
	return b.Buffer.Write(p)
}
func knowledgeCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	out := &knowledgeBoundedOutput{limit: knowledgeMaxTextBytes + 8192}
	cmd.Stdout = out
	// Do not retain diagnostics from an untrusted file parser in logs/jobs.
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		var missing *exec.Error
		if errors.As(err, &missing) {
			return nil, permanentKnowledgeError("file parser unavailable; install Poppler and Tesseract with eng/vie language data")
		}
		if ctx.Err() != nil {
			return nil, permanentKnowledgeError("file parsing exceeded 60 seconds; original retained")
		}
		return nil, permanentKnowledgeError("file parser rejected source or exceeded output limit; original retained")
	}
	return out.Bytes(), nil
}

var knowledgePagesPattern = regexp.MustCompile(`(?m)^Pages:\s+(\d+)`)

func extractKnowledgePDF(ctx context.Context, data []byte) (string, error) {
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return "", permanentKnowledgeError("invalid PDF header; original retained")
	}
	dir, err := os.MkdirTemp("", "multica-knowledge-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	input := filepath.Join(dir, "source.pdf")
	if err = os.WriteFile(input, data, 0600); err != nil {
		return "", err
	}
	info, err := knowledgeCommand(ctx, "pdfinfo", input)
	if err != nil {
		return "", err
	}
	match := knowledgePagesPattern.FindSubmatch(info)
	if len(match) != 2 {
		return "", permanentKnowledgeError("PDF page count unavailable; original retained")
	}
	count, _ := strconv.Atoi(string(match[1]))
	if count < 1 || count > knowledgeMaxPDFPages {
		return "", permanentKnowledgeError("PDF exceeds 60-page limit; original retained")
	}
	text, err := knowledgeCommand(ctx, "pdftotext", "-layout", "-enc", "UTF-8", input, "-")
	if err != nil {
		return "", err
	}
	pages := strings.Split(string(text), "\f")
	var out strings.Builder
	ocrPages := 0
	readable := 0
	for page := 1; page <= count; page++ {
		content := ""
		if page <= len(pages) {
			content = strings.TrimSpace(pages[page-1])
		}
		if len([]rune(content)) < 10 {
			ocrPages++
			if ocrPages > knowledgeMaxOCRPages {
				return "", permanentKnowledgeError("PDF exceeds 20 OCR-page limit; original retained")
			}
			prefix := filepath.Join(dir, "ocr")
			if _, err = knowledgeCommand(ctx, "pdftoppm", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-scale-to", "1800", "-singlefile", "-png", input, prefix); err != nil {
				return "", err
			}
			got, err := knowledgeCommand(ctx, "tesseract", prefix+".png", "stdout", "-l", "eng+vie")
			if err != nil {
				return "", err
			}
			content = strings.TrimSpace(string(got))
		}
		readable += len(strings.TrimSpace(content))
		fmt.Fprintf(&out, "[page %d]\n%s\n\n", page, content)
		if out.Len() > knowledgeMaxTextBytes {
			return "", permanentKnowledgeError("extracted text exceeds 256 KiB; original retained")
		}
	}
	if readable == 0 {
		return "", permanentKnowledgeError("PDF has no readable text after OCR; original retained")
	}
	return out.String(), nil
}
