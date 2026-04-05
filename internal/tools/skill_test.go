package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverSkillsFindsWorkspaceAndCodexHome(t *testing.T) {
	workDir := t.TempDir()
	codexHome := t.TempDir()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("HOME", home)

	mustWriteSkill(t, filepath.Join(workDir, ".codex", "skills", "local-a", skillFileName), "# Local A\nLocal description")
	mustWriteSkill(t, filepath.Join(workDir, ".agents", "skills", "local-b", skillFileName), "# Local B\nAnother description")
	mustWriteSkill(t, filepath.Join(codexHome, "skills", "home-a", skillFileName), "# Home A\nHome description")
	mustWriteSkill(t, filepath.Join(home, ".config", "tiny-agent-cli", "skills", "config-a", skillFileName), "# Config A\nConfig description")

	got, err := DiscoverSkills(workDir)
	if err != nil {
		t.Fatalf("discover skills: %v", err)
	}
	if len(got) != 7 {
		t.Fatalf("unexpected skill count: %#v", got)
	}

	joined := strings.ToLower(strings.Join(func() []string {
		names := make([]string, 0, len(got))
		for _, item := range got {
			names = append(names, item.Name)
		}
		return names
	}(), " "))
	for _, want := range []string{"home a", "local a", "local b", "config a", "memory", "planning", "hooks"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing skill %q in %#v", want, got)
		}
	}
}

func TestDiscoverSkillsSkipsFoldersWithoutSkillMarkdown(t *testing.T) {
	workDir := t.TempDir()
	t.Setenv("CODEX_HOME", t.TempDir())

	if err := os.MkdirAll(filepath.Join(workDir, ".codex", "skills", "broken"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got, err := DiscoverSkills(workDir)
	if err != nil {
		t.Fatalf("discover skills: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("expected bundled skills, got %#v", got)
	}
	for _, item := range got {
		if item.Path == filepath.Join(workDir, ".codex", "skills", "broken", skillFileName) {
			t.Fatalf("unexpected broken workspace skill in results: %#v", got)
		}
	}
}

func TestParseSkillMarkdownFallbacks(t *testing.T) {
	skill := parseSkillMarkdown("fallback", "/tmp/SKILL.md", "")
	if skill.Name != "fallback" {
		t.Fatalf("unexpected name: %#v", skill)
	}
	if skill.Description != "No description" {
		t.Fatalf("unexpected description: %#v", skill)
	}
}

func TestParseSkillMarkdownCapturesToolDefinitions(t *testing.T) {
	skill := parseSkillMarkdown("fallback", "/tmp/SKILL.md", "# Demo\nTools: read_file, write_file\nDescription line")
	if len(skill.ToolDefinitions) != 2 {
		t.Fatalf("unexpected tool definitions: %#v", skill.ToolDefinitions)
	}
}

func mustWriteSkill(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
