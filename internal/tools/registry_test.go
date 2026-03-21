package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRegistryIncludesTodoTools(t *testing.T) {
	r := NewRegistry(".", "bash", time.Second, nil)
	defs := r.Definitions()
	names := make(map[string]bool, len(defs))
	for _, def := range defs {
		names[def.Function.Name] = true
	}
	if !names["update_todo"] {
		t.Fatalf("update_todo missing from registry")
	}
	if !names["show_todo"] {
		t.Fatalf("show_todo missing from registry")
	}
	if !names["edit_file"] {
		t.Fatalf("edit_file missing from registry")
	}
}

func TestRegistryPreviewCompactsTodoItems(t *testing.T) {
	r := NewRegistry(".", "bash", time.Second, nil)
	raw := json.RawMessage(`{"items":[{"text":"inspect repo layout","status":"completed"},{"text":"patch agent loop","status":"in_progress"}]}`)
	got := r.Preview("update_todo", raw)
	if !strings.Contains(got, "inspect repo layout") {
		t.Fatalf("unexpected preview: %q", got)
	}
}
