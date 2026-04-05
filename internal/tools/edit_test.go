package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFileToolReplacesExactBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	tool := newEditFileTool(dir, mockApprover{writeAllowed: true})
	raw := json.RawMessage(`{"path":"note.txt","old_text":"beta","new_text":"BETA"}`)
	got, err := tool.Call(context.Background(), raw)
	if err != nil {
		t.Fatalf("edit failed: %v", err)
	}
	if !strings.Contains(got, "edited") {
		t.Fatalf("unexpected output: %q", got)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "alpha\nBETA\ngamma\n" {
		t.Fatalf("unexpected file contents: %q", data)
	}
}

func TestEditFileToolRejectsMissingOldText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	tool := newEditFileTool(dir, mockApprover{writeAllowed: true})
	raw := json.RawMessage(`{"path":"note.txt","old_text":"delta","new_text":"BETA"}`)
	_, err := tool.Call(context.Background(), raw)
	if err == nil || !strings.Contains(err.Error(), "old_text not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEditFileToolRejectsAmbiguousMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("beta\nbeta\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	tool := newEditFileTool(dir, mockApprover{writeAllowed: true})
	raw := json.RawMessage(`{"path":"note.txt","old_text":"beta","new_text":"BETA"}`)
	_, err := tool.Call(context.Background(), raw)
	if err == nil || !strings.Contains(err.Error(), "matched 2 times") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEditFileToolPreservesExistingPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nbeta\n"), 0o755); err != nil {
		t.Fatalf("write file: %v", err)
	}

	tool := newEditFileTool(dir, mockApprover{writeAllowed: true})
	raw := json.RawMessage(`{"path":"script.sh","old_text":"beta","new_text":"BETA"}`)
	if _, err := tool.Call(context.Background(), raw); err != nil {
		t.Fatalf("edit failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("unexpected mode: %o", got)
	}
}
