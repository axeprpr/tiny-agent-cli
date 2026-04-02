package agent

import (
	"strings"
	"testing"
)

func TestBuildSystemPromptIncludesRuntimeContext(t *testing.T) {
	prompt := BuildSystemPrompt(PromptContext{
		MemoryText:   "Project memory:\n- prefer Go",
		WorkDir:      "/repo",
		Shell:        "bash",
		ApprovalMode: "confirm",
		Model:        "gpt-test",
		ToolNames:    []string{"read_file", "run_command"},
		SessionMode:  "chat",
	})
	for _, want := range []string{
		"Runtime context:",
		"session_mode=chat",
		"workdir=/repo",
		"shell=bash",
		"model=gpt-test",
		"approval_mode=confirm",
		"Available tools:",
		"- read_file",
		"- run_command",
		"Project memory:",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildSystemPromptSkipsEmptySections(t *testing.T) {
	prompt := BuildSystemPrompt(PromptContext{})
	if strings.Contains(prompt, "Runtime context:") {
		t.Fatalf("unexpected runtime section in prompt: %s", prompt)
	}
	if strings.Contains(prompt, "Available tools:") {
		t.Fatalf("unexpected tools section in prompt: %s", prompt)
	}
}

func TestBuildSystemPromptIncludesRoleGuidance(t *testing.T) {
	prompt := BuildSystemPrompt(PromptContext{
		SessionMode: "background:verify",
	})
	if !strings.Contains(prompt, "Role guidance (verify):") {
		t.Fatalf("missing verify role guidance: %s", prompt)
	}
}
