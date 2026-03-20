package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
