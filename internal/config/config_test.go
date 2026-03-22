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
