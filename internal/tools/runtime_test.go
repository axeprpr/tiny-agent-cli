package tools

import (
	"encoding/json"
	"testing"

	"tiny-agent-cli/internal/model"
)

func TestValidateInputSchemaRequired(t *testing.T) {
	spec := model.FunctionSpec{
		Parameters: map[string]any{
			"type":     "object",
			"required": []string{"path", "content"},
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
			},
		},
	}
	err := validateInputSchema(json.RawMessage(`{"path":"a.txt"}`), spec)
	if err == nil {
		t.Fatalf("expected missing required error")
	}
}

func TestValidateInputSchemaTypeMismatch(t *testing.T) {
	spec := model.FunctionSpec{
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"timeout_seconds": map[string]any{"type": "integer"},
			},
		},
	}
	err := validateInputSchema(json.RawMessage(`{"timeout_seconds":"10"}`), spec)
	if err == nil {
		t.Fatalf("expected type mismatch error")
	}
}

func TestValidateToolInputWithSchema(t *testing.T) {
	params := map[string]any{
		"type":     "object",
		"required": []string{"query"},
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
	}
	if err := validateToolInput(json.RawMessage(`{"query":"hello"}`), params); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewDefaultHooksRespectsDisabledList(t *testing.T) {
	hooks := NewDefaultHooks(HookConfig{
		Enabled:  true,
		Disabled: []string{"command_safety"},
	})
	if len(hooks) != 0 {
		t.Fatalf("expected no hooks, got %d", len(hooks))
	}
}
