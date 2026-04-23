package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectDOCXTool(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "demo.docx")
	writeTestDOCX(t, path)

	tool := newInspectDOCXTool(dir)
	out, err := tool.Call(context.Background(), json.RawMessage(`{"path":"demo.docx","max_paragraphs":4}`))
	if err != nil {
		t.Fatalf("inspect docx: %v", err)
	}
	for _, want := range []string{"title: Demo Document", "creator: tiny-agent-cli", "headings:", "Release Notes", "table_count: 1", "image_count: 1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
}

func TestInspectPDFToolRejectsInvalidPDF(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.pdf"), []byte("not-a-pdf"), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	tool := newInspectPDFTool(dir)
	if _, err := tool.Call(context.Background(), json.RawMessage(`{"path":"broken.pdf"}`)); err == nil {
		t.Fatalf("expected invalid pdf error")
	}
}

func TestInspectPDFTool(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "demo.pdf")
	writeTestPDF(t, path, "Hello PDF")

	tool := newInspectPDFTool(dir)
	out, err := tool.Call(context.Background(), json.RawMessage(`{"path":"demo.pdf","max_pages":1,"max_chars":200}`))
	if err != nil {
		t.Fatalf("inspect pdf: %v", err)
	}
	for _, want := range []string{"page_count: 1", "text_preview:", "H e l l o P D F"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
}

func TestCheckWebappTool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Demo App</title></head><body><main>Hello release page</main></body></html>`))
	}))
	defer server.Close()

	tool := newCheckWebappTool()
	out, err := tool.Call(context.Background(), json.RawMessage(`{"url":"`+server.URL+`","title_contains":"Demo","contains_text":["release page"]}`))
	if err != nil {
		t.Fatalf("check webapp: %v", err)
	}
	if !strings.Contains(out, "title: Demo App") || !strings.Contains(out, "Hello release page") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestCheckWebappToolMissingText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head><title>Demo</title></head><body>ok</body></html>`))
	}))
	defer server.Close()

	tool := newCheckWebappTool()
	if _, err := tool.Call(context.Background(), json.RawMessage(`{"url":"`+server.URL+`","contains_text":["missing"]}`)); err == nil {
		t.Fatalf("expected contains_text failure")
	}
}

func writeTestDOCX(t *testing.T, path string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create docx: %v", err)
	}
	defer file.Close()

	writer := zip.NewWriter(file)
	writeZipFile := func(name, body string) {
		w, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create zip member %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("write zip member %s: %v", name, err)
		}
	}
	writeZipFile("[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"></Types>`)
	writeZipFile("docProps/core.xml", `<?xml version="1.0" encoding="UTF-8"?><cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Demo Document</dc:title><dc:creator>tiny-agent-cli</dc:creator></cp:coreProperties>`)
	writeZipFile("word/document.xml", `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Release Notes</w:t></w:r></w:p>
    <w:p><w:r><w:t>First paragraph.</w:t></w:r></w:p>
    <w:tbl><w:tr><w:tc><w:p><w:r><w:t>Cell</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
  </w:body>
</w:document>`)
	writeZipFile("word/media/image1.png", "png")
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
}

func writeTestPDF(t *testing.T, path, text string) {
	t.Helper()
	stream := fmt.Sprintf("BT /F1 18 Tf 50 100 Td (%s) Tj ET", text)
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 144] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for i, obj := range objects {
		offsets[i+1] = buf.Len()
		buf.WriteString(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, obj))
	}
	xrefOffset := buf.Len()
	buf.WriteString("xref\n")
	buf.WriteString(fmt.Sprintf("0 %d\n", len(objects)+1))
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= len(objects); i++ {
		buf.WriteString(fmt.Sprintf("%010d 00000 n \n", offsets[i]))
	}
	buf.WriteString("trailer\n")
	buf.WriteString(fmt.Sprintf("<< /Size %d /Root 1 0 R >>\n", len(objects)+1))
	buf.WriteString("startxref\n")
	buf.WriteString(fmt.Sprintf("%d\n%%%%EOF\n", xrefOffset))
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
}
