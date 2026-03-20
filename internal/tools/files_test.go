package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type mockApprover struct {
	writeAllowed bool
}

func (m mockApprover) ApproveCommand(context.Context, string) (bool, error) {
	return true, nil
}

func (m mockApprover) ApproveWrite(context.Context, string, string) (bool, error) {
	return m.writeAllowed, nil
}

func (m mockApprover) Mode() string {
	return ApprovalConfirm
}

func (m mockApprover) SetMode(string) error {
	return nil
}

func TestWriteFileToolRejectsWhenApproverDenies(t *testing.T) {
	dir := t.TempDir()
	tool := newWriteFileTool(dir, mockApprover{writeAllowed: false})

	raw := json.RawMessage(`{"path":"note.txt","content":"hello"}`)
	_, err := tool.Call(context.Background(), raw)
	if err == nil || err.Error() != "file write rejected by user" {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(dir, "note.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("file should not exist, got err=%v", statErr)
	}
}

func TestWriteFileToolWritesWhenApproved(t *testing.T) {
	dir := t.TempDir()
	tool := newWriteFileTool(dir, mockApprover{writeAllowed: true})

	raw := json.RawMessage(`{"path":"note.txt","content":"hello"}`)
	if _, err := tool.Call(context.Background(), raw); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "note.txt"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("unexpected file contents: %q", data)
	}
}

func TestReadFileToolRejectsBinaryContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tacli")
	if err := os.WriteFile(path, []byte{0x7f, 'E', 'L', 'F', 0x00, 0x01}, 0o644); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	tool := newReadFileTool(dir)
	raw := json.RawMessage(`{"path":"tacli"}`)
	got, err := tool.Call(context.Background(), raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "binary file: tacli") {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestReadFileToolTruncatesLargeText(t *testing.T) {
	dir := t.TempDir()
	text := strings.Repeat("hello world\n", 8000)
	path := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	tool := newReadFileTool(dir)
	raw := json.RawMessage(`{"path":"big.txt"}`)
	got, err := tool.Call(context.Background(), raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "...[truncated; file size") {
		t.Fatalf("expected truncation marker, got: %q", got)
	}
}
