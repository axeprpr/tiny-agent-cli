package main

import (
	"bufio"
	"os"
	"strings"
	"testing"
	"time"

	"tiny-agent-cli/internal/agent"
	"tiny-agent-cli/internal/config"
	"tiny-agent-cli/internal/model"
	"tiny-agent-cli/internal/tools"
)

type staticContextProvider string

func (p staticContextProvider) SystemMemory() string { return string(p) }

func TestRuntimeKernelNewSessionIncludesMemoryAndContract(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{
		Model:          "test-model",
		WorkDir:        dir,
		StateDir:       dir,
		ContextWindow:  32768,
		CommandTimeout: time.Second,
		Shell:          "bash",
		ApprovalMode:   tools.ApprovalConfirm,
	}
	loop := agent.New(chatClientStub{}, tools.NewRegistry(dir, "bash", time.Second, nil, nil), 32768, nil)
	if err := loop.ReplaceTaskContract(tools.TaskContract{
		Objective: "Ship runtime cleanup",
		Deliverables: []tools.ContractItem{
			{Text: "Extract runtime kernel", Status: "pending"},
		},
		AcceptanceChecks: []tools.ContractItem{
			{Text: "Tests stay green", Status: "pending"},
		},
	}); err != nil {
		t.Fatalf("replace task contract: %v", err)
	}

	kernel := newRuntimeKernel(cfg, tools.NewTerminalApprover(bufio.NewReader(strings.NewReader("")), os.Stderr, tools.ApprovalConfirm, true), os.Stderr, nil, nil)
	session := kernel.newSession(loop, "chat", staticContextProvider("Persistent memory note"))
	messages := session.Messages()
	if len(messages) == 0 {
		t.Fatalf("expected session messages")
	}
	prompt := messages[0].Content.(string)
	if !strings.Contains(prompt, "Persistent memory note") {
		t.Fatalf("expected memory text in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "Ship runtime cleanup") {
		t.Fatalf("expected task contract in prompt, got %q", prompt)
	}
}

func TestRuntimeKernelRefreshSessionPromptPreservesConversation(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{
		Model:          "test-model",
		WorkDir:        dir,
		StateDir:       dir,
		ContextWindow:  32768,
		CommandTimeout: time.Second,
		Shell:          "bash",
		ApprovalMode:   tools.ApprovalConfirm,
	}
	loop := agent.New(chatClientStub{}, tools.NewRegistry(dir, "bash", time.Second, nil, nil), 32768, nil)
	kernel := newRuntimeKernel(cfg, tools.NewTerminalApprover(bufio.NewReader(strings.NewReader("")), os.Stderr, tools.ApprovalConfirm, true), os.Stderr, nil, nil)
	session := kernel.newSession(loop, "chat", staticContextProvider("old memory"))
	session.ReplaceMessages(append(session.Messages(),
		model.Message{Role: "user", Content: "hello"},
		model.Message{Role: "assistant", Content: "world"},
	))

	refreshed := kernel.refreshSessionPrompt(session, loop, "chat", staticContextProvider("new memory"))
	messages := refreshed.Messages()
	if len(messages) != 3 {
		t.Fatalf("expected refreshed session to preserve messages, got %d", len(messages))
	}
	if !strings.Contains(messages[0].Content.(string), "new memory") {
		t.Fatalf("expected refreshed prompt to include new memory, got %q", messages[0].Content)
	}
	if messages[1].Role != "user" || messages[2].Role != "assistant" {
		t.Fatalf("expected non-system conversation to be preserved, got %#v", messages)
	}
}

func TestMemoryContextProviderRendersSystemMemory(t *testing.T) {
	provider := newMemoryContextProvider(
		[]string{"Global note"},
		[]string{"Team note"},
		[]string{"Project note"},
	)
	got := provider.SystemMemory()
	if !strings.Contains(got, "Persistent context memory:") || !strings.Contains(got, "Global notes:") || !strings.Contains(got, "Team notes:") || !strings.Contains(got, "Project notes:") {
		t.Fatalf("expected grouped memory sections, got %q", got)
	}
}
