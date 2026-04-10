package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type cliHarnessResult struct {
	code   int
	stdout string
	stderr string
}

func runCLIHarness(t *testing.T, workDir string, args []string, stdin []byte) cliHarnessResult {
	t.Helper()

	stateDir := filepath.Join(workDir, ".tacli")
	oldWorkDir := os.Getenv("AGENT_WORKDIR")
	oldStateDir := os.Getenv("AGENT_STATE_DIR")
	oldStdin, oldStdout, oldStderr := os.Stdin, os.Stdout, os.Stderr

	if err := os.Setenv("AGENT_WORKDIR", workDir); err != nil {
		t.Fatalf("set AGENT_WORKDIR: %v", err)
	}
	if err := os.Setenv("AGENT_STATE_DIR", stateDir); err != nil {
		t.Fatalf("set AGENT_STATE_DIR: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("AGENT_WORKDIR", oldWorkDir)
		_ = os.Setenv("AGENT_STATE_DIR", oldStateDir)
		os.Stdin, os.Stdout, os.Stderr = oldStdin, oldStdout, oldStderr
	})

	stdinFile, err := os.CreateTemp(t.TempDir(), "cli-stdin-*")
	if err != nil {
		t.Fatalf("create stdin temp file: %v", err)
	}
	if _, err := stdinFile.Write(stdin); err != nil {
		t.Fatalf("write stdin temp file: %v", err)
	}
	if _, err := stdinFile.Seek(0, 0); err != nil {
		t.Fatalf("rewind stdin temp file: %v", err)
	}
	defer stdinFile.Close()

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	defer stdoutR.Close()
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	defer stderrR.Close()

	os.Stdin, os.Stdout, os.Stderr = stdinFile, stdoutW, stderrW
	code := run(args)
	_ = stdoutW.Close()
	_ = stderrW.Close()

	stdoutBytes := new(bytes.Buffer)
	if _, err := stdoutBytes.ReadFrom(stdoutR); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	stderrBytes := new(bytes.Buffer)
	if _, err := stderrBytes.ReadFrom(stderrR); err != nil {
		t.Fatalf("read stderr: %v", err)
	}

	return cliHarnessResult{
		code:   code,
		stdout: stdoutBytes.String(),
		stderr: stderrBytes.String(),
	}
}

func TestCLIParityControlPlaneScenario(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example.com/parity\n\ngo 1.25.1\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "plan.md"), []byte("# parity plan"), 0o644); err != nil {
		t.Fatalf("write plan.md: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workDir, ".agents", "skills", "demo"), 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, ".agents", "skills", "demo", "SKILL.md"), []byte("# Demo\nParity scenario\nTools: read_file"), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}

	result := runCLIHarness(t, workDir, []string{"init", "--workdir", workDir}, nil)
	if result.code != 0 || !strings.Contains(result.stdout, "CLAW.md") {
		t.Fatalf("init failed: %#v", result)
	}

	result = runCLIHarness(t, workDir, []string{"plan", "--workdir", workDir}, nil)
	if result.code != 0 || strings.TrimSpace(result.stdout) != "# parity plan" {
		t.Fatalf("plan failed: %#v", result)
	}

	result = runCLIHarness(t, workDir, []string{"skills", "--workdir", workDir}, nil)
	if result.code != 0 || !strings.Contains(result.stdout, "Demo [local]: Parity scenario tools=read_file") {
		t.Fatalf("skills failed: %#v", result)
	}

	result = runCLIHarness(t, workDir, []string{"status", "--workdir", workDir}, nil)
	if result.code != 0 {
		t.Fatalf("status failed: %#v", result)
	}
	for _, want := range []string{
		"plan=" + filepath.Join(workDir, "plan.md"),
		"skills=",
		"sessions=0",
		"command_rules=0",
	} {
		if !strings.Contains(result.stdout, want) {
			t.Fatalf("expected status output to contain %q, got %q", want, result.stdout)
		}
	}
}

func TestCLIParityChatPolicyScenario(t *testing.T) {
	workDir := t.TempDir()
	stdin := []byte("/policy command add deny git push *\x00/policy\x00/status\n")
	result := runCLIHarness(t, workDir, []string{
		"chat",
		"--workdir", workDir,
		"--base-url", "http://example.test/v1",
		"--model", "test-model",
		"--api-key", "test-key",
		"--session", "policy-parity",
	}, stdin)
	if result.code != 0 {
		t.Fatalf("chat policy scenario failed: %#v", result)
	}
	for _, want := range []string{
		"1. deny git push *",
		"command_rules=1",
		"session=policy-parity",
	} {
		if !strings.Contains(result.stderr, want) {
			t.Fatalf("expected stderr to contain %q, got %q", want, result.stderr)
		}
	}
}

func TestCLIParitySessionResumeScenario(t *testing.T) {
	workDir := t.TempDir()

	result := runCLIHarness(t, workDir, []string{
		"chat",
		"--workdir", workDir,
		"--base-url", "http://example.test/v1",
		"--model", "test-model",
		"--api-key", "test-key",
		"--session", "resume-parity",
	}, []byte("/memory remember alpha\x00/session save\n"))
	if result.code != 0 {
		t.Fatalf("first chat run failed: %#v", result)
	}

	result = runCLIHarness(t, workDir, []string{
		"chat",
		"--workdir", workDir,
		"--base-url", "http://example.test/v1",
		"--model", "test-model",
		"--api-key", "test-key",
		"--session", "resume-parity",
	}, []byte("/memory show\n"))
	if result.code != 0 {
		t.Fatalf("second chat run failed: %#v", result)
	}
	if !strings.Contains(result.stderr, "alpha") || !strings.Contains(result.stderr, "project_notes=1") {
		t.Fatalf("expected resumed memory note, got %#v", result)
	}
}
