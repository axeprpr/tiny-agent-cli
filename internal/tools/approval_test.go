package tools

import (
	"bufio"
	"context"
	"strings"
	"testing"
)

func TestTerminalApproverCachesApprovedCommand(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("y\n"))
	var out strings.Builder
	a := NewTerminalApprover(reader, &out, ApprovalConfirm, true)

	ok, err := a.ApproveCommand(context.Background(), "echo hi")
	if err != nil || !ok {
		t.Fatalf("first approval failed: ok=%v err=%v", ok, err)
	}
	ok, err = a.ApproveCommand(context.Background(), "echo hi")
	if err != nil || !ok {
		t.Fatalf("second approval failed: ok=%v err=%v", ok, err)
	}

	if count := strings.Count(out.String(), "Command approval required:"); count != 1 {
		t.Fatalf("expected one prompt, got %d\n%s", count, out.String())
	}
}

func TestTerminalApproverCachesApprovedWrite(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("y\n"))
	var out strings.Builder
	a := NewTerminalApprover(reader, &out, ApprovalConfirm, true)

	ok, err := a.ApproveWrite(context.Background(), "/tmp/test.txt", "hello")
	if err != nil || !ok {
		t.Fatalf("first approval failed: ok=%v err=%v", ok, err)
	}
	ok, err = a.ApproveWrite(context.Background(), "/tmp/test.txt", "hello")
	if err != nil || !ok {
		t.Fatalf("second approval failed: ok=%v err=%v", ok, err)
	}

	if count := strings.Count(out.String(), "File write approval required:"); count != 1 {
		t.Fatalf("expected one prompt, got %d\n%s", count, out.String())
	}
}
