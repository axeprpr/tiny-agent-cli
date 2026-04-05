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
	t.Setenv("AGENT_HOOKS_ENABLED", "false")
	t.Setenv("AGENT_HOOKS_DISABLED", "command_safety, custom ")

	cfg := FromEnv()
	if cfg.Hooks.Enabled {
		t.Fatalf("expected hooks to be disabled")
	}
	if got, want := len(cfg.Hooks.Disabled), 2; got != want {
		t.Fatalf("unexpected disabled hooks length: got %d want %d", got, want)
	}
	if cfg.Hooks.Disabled[0] != "command_safety" || cfg.Hooks.Disabled[1] != "custom" {
		t.Fatalf("unexpected disabled hooks: %#v", cfg.Hooks.Disabled)
	}
}
