package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiny-agent-cli/internal/tools"
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

func TestCreatedWorkspaceFilesSortsNewestFirst(t *testing.T) {
	before := map[string]workspaceFileState{
		"a.txt": {Size: 1, ModUnix: 10},
	}
	after := map[string]workspaceFileState{
		"a.txt": {Size: 2, ModUnix: 20},
		"b.txt": {Size: 1, ModUnix: 30},
	}
	got := createdWorkspaceFiles(before, after)
	if len(got) != 1 {
		t.Fatalf("unexpected changed files: %#v", got)
	}
	if got[0] != "b.txt" {
		t.Fatalf("unexpected order: %#v", got)
	}
}

func TestCollectDashboardArtifactPathsMergesExplicitAndCreated(t *testing.T) {
	got := collectDashboardArtifactPaths(map[string]struct{}{
		"build/report.pptx": {},
		"docs/plan.md":      {},
	}, []string{"build/report.pptx", "exports/summary.pdf"})
	if len(got) != 3 {
		t.Fatalf("unexpected artifact paths: %#v", got)
	}
	for _, want := range []string{"build/report.pptx", "docs/plan.md", "exports/summary.pdf"} {
		found := false
		for _, item := range got {
			if item == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing artifact path %q in %#v", want, got)
		}
	}
}

func TestToolArtifactPathsOnlyTracksExplicitFileWrites(t *testing.T) {
	got := toolArtifactPaths(tools.ToolAuditEvent{
		Tool:      "write_file",
		Status:    "ok",
		InputJSON: `{"path":"slides/output.pptx","content":"..."}`,
	})
	if len(got) != 1 || got[0] != "slides/output.pptx" {
		t.Fatalf("unexpected tracked artifact paths: %#v", got)
	}
	if got := toolArtifactPaths(tools.ToolAuditEvent{
		Tool:      "run_command",
		Status:    "ok",
		InputJSON: `{"command":"touch out.txt"}`,
	}); len(got) != 0 {
		t.Fatalf("expected no artifact paths for run_command, got %#v", got)
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
