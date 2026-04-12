package config

import (
	"os"
	"testing"
)

func TestFromEnvLeavesMaxStepsDisabledByDefault(t *testing.T) {
	t.Setenv("AGENT_MAX_STEPS", "")

	cfg := FromEnv()
	if cfg.MaxSteps != defaultMaxSteps {
		t.Fatalf("unexpected max steps: got %d want %d", cfg.MaxSteps, defaultMaxSteps)
	}
}

func TestDefaultStateDirPrefersLegacyDirWhenPresent(t *testing.T) {
	dir := t.TempDir()
	legacy := dir + "/.onek-agent"
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatalf("mkdir legacy dir: %v", err)
	}

	if got := defaultStateDir(dir); got != legacy {
		t.Fatalf("unexpected state dir: got %q want %q", got, legacy)
	}
}

func TestFromEnvLoadsHookConfig(t *testing.T) {
	t.Setenv("AGENT_PRE_TOOL_USE_HOOKS", "printf 'pre one'\nprintf 'pre two'")
	t.Setenv("AGENT_POST_TOOL_USE_HOOKS", "printf 'post one'")

	cfg := FromEnv()
	if got, want := len(cfg.Hooks.PreToolUse), 2; got != want {
		t.Fatalf("unexpected pre hook count: got %d want %d", got, want)
	}
	if got, want := len(cfg.Hooks.PostToolUse), 1; got != want {
		t.Fatalf("unexpected post hook count: got %d want %d", got, want)
	}
	if cfg.Hooks.PreToolUse[0] != "printf 'pre one'" || cfg.Hooks.PreToolUse[1] != "printf 'pre two'" {
		t.Fatalf("unexpected pre hooks: %#v", cfg.Hooks.PreToolUse)
	}
	if cfg.Hooks.PostToolUse[0] != "printf 'post one'" {
		t.Fatalf("unexpected post hooks: %#v", cfg.Hooks.PostToolUse)
	}
}

func TestFromEnvLoadsSettingsSyncConfig(t *testing.T) {
	t.Setenv("AGENT_SETTINGS_ENDPOINT", "https://settings.example/api")
	t.Setenv("AGENT_SETTINGS_SYNC", "false")
	t.Setenv("AGENT_TEAM", "eng")

	cfg := FromEnv()
	if cfg.SettingsURL != "https://settings.example/api" {
		t.Fatalf("unexpected settings url: %q", cfg.SettingsURL)
	}
	if cfg.SettingsSync {
		t.Fatalf("expected settings sync to be disabled")
	}
	if cfg.Team != "eng" {
		t.Fatalf("unexpected team: %q", cfg.Team)
	}
}

func TestFromEnvUsesModelAwareContextWindowDefaults(t *testing.T) {
	t.Setenv("MODEL_CONTEXT_WINDOW", "")
	t.Setenv("MODEL_NAME", "gpt-5-mini")

	cfg := FromEnv()
	if cfg.ContextWindow != 400000 {
		t.Fatalf("unexpected context window: got %d want %d", cfg.ContextWindow, 400000)
	}
}

func TestFromEnvPrefersExplicitContextWindowOverride(t *testing.T) {
	t.Setenv("MODEL_NAME", "gpt-5-mini")
	t.Setenv("MODEL_CONTEXT_WINDOW", "123456")

	cfg := FromEnv()
	if cfg.ContextWindow != 123456 {
		t.Fatalf("unexpected context window override: got %d want %d", cfg.ContextWindow, 123456)
	}
}
