package model

import "testing"

func TestContentStringFromArray(t *testing.T) {
	value := []any{
		map[string]any{"text": "hello"},
		map[string]any{"text": "world"},
	}

	got := ContentString(value)
	if got != "hello\nworld" {
		t.Fatalf("unexpected content string: %q", got)
	}
}
