package tools

import (
	"context"
	"encoding/json"
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
