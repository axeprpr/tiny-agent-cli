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

func TestBuildSystemPromptIncludesSkills(t *testing.T) {
	prompt := BuildSystemPrompt(PromptContext{
		Skills: []PromptSkill{
			{Name: "playwright", Description: "Browser automation", Path: "/skills/playwright/SKILL.md", Instructions: "Use browser-oriented verification steps."},
		},
	})
	for _, want := range []string{"Available skills:", "playwright: Browser automation", "Skill instructions:", "Use browser-oriented verification steps."} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildSystemPromptIncludesCapabilities(t *testing.T) {
	prompt := BuildSystemPrompt(PromptContext{
		Capabilities: []PromptCapability{{
			Name:        "repo-research",
			Description: "Map a repository before implementation.",
			When:        "Use for onboarding.",
			Roles:       []string{"explore", "plan"},
			Tools:       []string{"read_file", "run_command"},
		}},
	})
	for _, want := range []string{"Capability packs:", "repo-research: Map a repository", "roles=explore,plan", "tools=read_file,run_command"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildSystemPromptIncludesGitContext(t *testing.T) {
	prompt := BuildSystemPrompt(PromptContext{
		GitBranch: "main",
		GitStatus: "## main\n M prompt.go",
	})
	for _, want := range []string{"Project context:", "git_branch=main", "git_status:", "## main", "M prompt.go"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildSystemPromptIncludesInstructionFiles(t *testing.T) {
	prompt := BuildSystemPrompt(PromptContext{
		Instructions: []PromptInstructionFile{
			{Path: "/repo/CLAW.md", Content: "Follow repo rules\nRun tests"},
		},
	})
	for _, want := range []string{"Instruction files:", "/repo/CLAW.md:", "Follow repo rules", "Run tests"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildSystemPromptIncludesTaskContract(t *testing.T) {
	prompt := BuildSystemPrompt(PromptContext{
		TaskContract: "task_kind=webapp_with_deploy\nobjective=Ship the embedded app\nacceptance_checks:\n1. [todo] GET / returns app html",
	})
	for _, want := range []string{
		"Current task contract:",
		"task_kind=webapp_with_deploy",
		"objective=Ship the embedded app",
		"GET / returns app html",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildSystemPromptIncludesCoreToolSelectionGuidance(t *testing.T) {
	prompt := BuildSystemPrompt(PromptContext{})
	for _, want := range []string{
		"If the user asks for file or code contents, use a file-reading tool",
		"If the user asks for a repository, package, or official documentation page, prefer GitHub",
		"After any tool call, check whether the evidence is sufficient before answering.",
		"must create a concrete plan before the first mutating action",
		"If a mutating action fails, do not immediately try a different mutating path.",
		"If the user rejects a write, command, or permission request, stop and return control to the user.",
		"blocked work without a terminal handoff",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}
