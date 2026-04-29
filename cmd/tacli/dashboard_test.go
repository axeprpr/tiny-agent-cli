package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiny-agent-cli/internal/config"
)

func TestResolveWorkspacePath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	abs, rel, err := resolveWorkspacePath(root, "note.txt")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if rel != "note.txt" {
		t.Fatalf("unexpected relative path: %q", rel)
	}
	if abs != filepath.Join(root, "note.txt") {
		t.Fatalf("unexpected absolute path: %q", abs)
	}
	if _, _, err := resolveWorkspacePath(root, "../escape.txt"); err == nil {
		t.Fatalf("expected escape path to fail")
	}
}

func TestBuildDashboardTaskIncludesAttachments(t *testing.T) {
	got := buildDashboardTask("Summarize this.", []dashboardFile{
		{Path: "docs/spec.md"},
		{Path: "tmp/report.pdf"},
	})
	for _, want := range []string{
		"Attached files are available in the workspace:",
		"- docs/spec.md",
		"- tmp/report.pdf",
		"Summarize this.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestBuildDashboardTaskContentIncludesImageParts(t *testing.T) {
	root := t.TempDir()
	pngData, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a2ioAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pic.png"), pngData, 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	got, ok := buildDashboardTaskContent(root, "describe it", []dashboardFile{{Path: "pic.png", Name: "pic.png"}})
	if !ok {
		t.Fatalf("expected multimodal content to be built")
	}
	if len(got) != 2 {
		t.Fatalf("unexpected multimodal parts: %#v", got)
	}
	if got[0].Text == "" || got[1].ImageURL == nil || !strings.HasPrefix(got[1].ImageURL.URL, "data:image/png;base64,") {
		t.Fatalf("unexpected multimodal payload: %#v", got)
	}
}

func TestRenderMarkdownHTMLSanitizesScripts(t *testing.T) {
	got := renderMarkdownHTML("hello<script>alert(1)</script>")
	if strings.Contains(strings.ToLower(got), "<script") {
		t.Fatalf("expected sanitized html, got %q", got)
	}
	if !strings.Contains(got, "hello") {
		t.Fatalf("expected content to remain, got %q", got)
	}
}

func TestFileLooksText(t *testing.T) {
	dir := t.TempDir()
	textPath := filepath.Join(dir, "demo.txt")
	binPath := filepath.Join(dir, "demo.bin")
	if err := os.WriteFile(textPath, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("write text: %v", err)
	}
	if err := os.WriteFile(binPath, []byte{0x00, 0x01, 0x02, 0x03}, 0o644); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	if !fileLooksText(textPath) {
		t.Fatalf("expected text file to be previewable")
	}
	if fileLooksText(binPath) {
		t.Fatalf("expected binary file to be rejected")
	}
}

func TestBuildDashboardFileSetsPreviewURLForHTML(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "preview.html"), []byte("<!doctype html><h1>demo</h1>"), 0o644); err != nil {
		t.Fatalf("write html: %v", err)
	}
	file, err := buildDashboardFile(root, "preview.html")
	if err != nil {
		t.Fatalf("build file: %v", err)
	}
	if file.PreviewURL != "/api/preview/preview.html" {
		t.Fatalf("unexpected preview url: %q", file.PreviewURL)
	}
}

func TestHandleFilePreviewServesHTML(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "preview.html"), []byte("<!doctype html><h1>demo</h1>"), 0o644); err != nil {
		t.Fatalf("write html: %v", err)
	}
	server := &dashboardServer{
		runtime: &chatRuntime{cfg: config.Config{WorkDir: root}},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/preview/preview.html", nil)
	rec := httptest.NewRecorder()
	server.handleFilePreview(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("unexpected content type: %q", got)
	}
	if !strings.Contains(rec.Body.String(), "<h1>demo</h1>") {
		t.Fatalf("unexpected body: %q", rec.Body.String())
	}
}
