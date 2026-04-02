package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateTodoToolStoresAndFormatsItems(t *testing.T) {
	store := newTodoStore()
	update := newUpdateTodoTool(store)
	show := newShowTodoTool(store)

	raw := json.RawMessage(`{"items":[{"text":"inspect repo layout","status":"completed"},{"text":"patch agent loop","status":"in_progress"},{"text":"run tests","status":"pending"}]}`)
	got, err := update.Call(context.Background(), raw)
	if err != nil {
		t.Fatalf("update todo failed: %v", err)
	}
	if !strings.Contains(got, "[done] inspect repo layout") {
		t.Fatalf("unexpected update output: %q", got)
	}
	if !strings.Contains(got, "[doing] patch agent loop") {
		t.Fatalf("unexpected update output: %q", got)
	}

	showText, err := show.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("show todo failed: %v", err)
	}
	if showText != got {
		t.Fatalf("show output mismatch:\nupdate=%q\nshow=%q", got, showText)
	}
}

func TestUpdateTodoToolRejectsMultipleInProgressItems(t *testing.T) {
	store := newTodoStore()
	update := newUpdateTodoTool(store)

	raw := json.RawMessage(`{"items":[{"text":"a","status":"in_progress"},{"text":"b","status":"in_progress"}]}`)
	_, err := update.Call(context.Background(), raw)
	if err == nil || !strings.Contains(err.Error(), "only one todo item") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestShowTodoToolEmpty(t *testing.T) {
	store := newTodoStore()
	show := newShowTodoTool(store)
	got, err := show.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("show todo failed: %v", err)
	}
	if got != "(no todo items)" {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestTodoStorePersistsTaskV2File(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks-v2.json")
	store := newTodoStoreWithPath(path)
	if err := store.Replace([]TodoItem{{Text: "inspect", Status: "in_progress"}}); err != nil {
		t.Fatalf("replace todo: %v", err)
	}

	reloaded := newTodoStoreWithPath(path)
	items := reloaded.Items()
	if len(items) != 1 || items[0].Text != "inspect" || items[0].Status != "in_progress" {
		t.Fatalf("unexpected reloaded items: %#v", items)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read task file: %v", err)
	}
	if !strings.Contains(string(raw), `"version": 2`) {
		t.Fatalf("expected v2 version in task file, got %s", string(raw))
	}
}

func TestTodoStoreLoadsLegacyArrayFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks-v2.json")
	legacy := `[{"text":"run tests","status":"pending"}]`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}
	store := newTodoStoreWithPath(path)
	items := store.Items()
	if len(items) != 1 || items[0].Text != "run tests" || items[0].Status != "pending" {
		t.Fatalf("unexpected items from legacy file: %#v", items)
	}
}
