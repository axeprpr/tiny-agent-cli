package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"tiny-agent-cli/internal/tasks"
)

func TestTaskToolsCRUD(t *testing.T) {
	store := tasks.New(filepath.Join(t.TempDir(), "tasks.json"))
	create := newCreateTaskTool(store)
	got, err := create.Call(context.Background(), []byte(`{"title":"ship it","details":"soon"}`))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if !strings.Contains(got, "task-001") {
		t.Fatalf("unexpected create output: %q", got)
	}

	update := newUpdateTaskTool(store)
	got, err = update.Call(context.Background(), []byte(`{"id":"task-001","status":"in_progress"}`))
	if err != nil {
		t.Fatalf("update task: %v", err)
	}
	if !strings.Contains(got, "[in_progress]") {
		t.Fatalf("unexpected update output: %q", got)
	}

	list := newListTasksTool(store)
	got, err = list.Call(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if !strings.Contains(got, "task-001") {
		t.Fatalf("unexpected list output: %q", got)
	}

	del := newDeleteTaskTool(store)
	got, err = del.Call(context.Background(), []byte(`{"id":"task-001"}`))
	if err != nil {
		t.Fatalf("delete task: %v", err)
	}
	if !strings.Contains(got, "deleted task-001") {
		t.Fatalf("unexpected delete output: %q", got)
	}
}
