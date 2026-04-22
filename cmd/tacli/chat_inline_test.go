package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestInlineShellSubmitClearsInputAndStartsTask(t *testing.T) {
	submitCh := make(chan string, 1)
	m := newInlineShellModel(submitCh)
	m.input.SetValue("你好")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(inlineShellModel)

	if m.input.Value() != "" {
		t.Fatalf("expected input to reset after submit, got %q", m.input.Value())
	}
	if cmd == nil {
		t.Fatalf("expected submit command")
	}
	msg := cmd()
	if _, ok := msg.(inlineShellTaskStartedMsg); !ok {
		t.Fatalf("expected task started message, got %T", msg)
	}
	select {
	case task := <-submitCh:
		if task != "你好" {
			t.Fatalf("unexpected submitted task: %q", task)
		}
	default:
		t.Fatalf("expected submitted task on channel")
	}
}

func TestInlineShellIgnoresTypingWhileBusy(t *testing.T) {
	submitCh := make(chan string, 1)
	m := newInlineShellModel(submitCh)
	m.busy = true
	m.input.SetValue("seed")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = updated.(inlineShellModel)

	if got := m.input.Value(); got != "seed" {
		t.Fatalf("expected busy shell to ignore typing, got %q", got)
	}
}

