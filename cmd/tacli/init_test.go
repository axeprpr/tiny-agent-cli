package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiny-agent-cli/internal/config"
)

func TestInitializeRepoCreatesArtifactsAndGoGuidance(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/demo\n\ngo 1.25.1\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "cmd"), 0o755); err != nil {
		t.Fatalf("mkdir cmd: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "internal"), 0o755); err != nil {
		t.Fatalf("mkdir internal: %v", err)
	}

	report, err := initializeRepo(dir)
	if err != nil {
		t.Fatalf("initializeRepo returned error: %v", err)
	}
	if report.ProjectRoot != dir {
		t.Fatalf("unexpected project root: got %q want %q", report.ProjectRoot, dir)
	}

	gotStatuses := make(map[string]initStatus, len(report.Artifacts))
	for _, artifact := range report.Artifacts {
		gotStatuses[artifact.Name] = artifact.Status
	}
	if gotStatuses[".claw/"] != initStatusCreated {
		t.Fatalf("expected .claw/ to be created, got %v", gotStatuses[".claw/"])
	}
	if gotStatuses[".gitignore"] != initStatusCreated {
		t.Fatalf("expected .gitignore to be created, got %v", gotStatuses[".gitignore"])
	}
	if gotStatuses["CLAW.md"] != initStatusCreated {
		t.Fatalf("expected CLAW.md to be created, got %v", gotStatuses["CLAW.md"])
	}

	clawMD, err := os.ReadFile(filepath.Join(dir, "CLAW.md"))
	if err != nil {
		t.Fatalf("read CLAW.md: %v", err)
	}
	clawText := string(clawMD)
	for _, want := range []string{"Languages: Go.", "`go test ./...`", "`cmd/`", "`internal/`", "`CLAW.local.md`"} {
		if !strings.Contains(clawText, want) {
			t.Fatalf("expected CLAW.md to contain %q, got %q", want, clawText)
		}
	}

	gitignore, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	gitignoreText := string(gitignore)
	for _, want := range []string{initGitignoreComment, ".tacli/", "CLAW.local.md"} {
		if !strings.Contains(gitignoreText, want) {
			t.Fatalf("expected .gitignore to contain %q, got %q", want, gitignoreText)
		}
	}
}

func TestInitializeRepoIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := initializeRepo(dir); err != nil {
		t.Fatalf("first initializeRepo returned error: %v", err)
	}

	report, err := initializeRepo(dir)
	if err != nil {
		t.Fatalf("second initializeRepo returned error: %v", err)
	}
	for _, artifact := range report.Artifacts {
		if artifact.Status != initStatusSkipped {
			t.Fatalf("expected %s to be skipped on second init, got %s", artifact.Name, artifact.Status.label())
		}
	}

	gitignore, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	for _, want := range initGitignoreEntries {
		if count := strings.Count(string(gitignore), want); count != 1 {
			t.Fatalf("expected %q once in .gitignore, got %d", want, count)
		}
	}
}

func TestRunInitCommandCreatesScaffold(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_WORKDIR", dir)
	t.Setenv("AGENT_STATE_DIR", filepath.Join(dir, ".tacli"))
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

	oldStdout, oldStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutW, stderrW
	defer func() {
		os.Stdout, os.Stderr = oldStdout, oldStderr
	}()

	code := run([]string{"init", "--workdir", dir})
	_ = stdoutW.Close()
	_ = stderrW.Close()

	stdoutBytes, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	stderrBytes, err := io.ReadAll(stderrR)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if code != 0 {
		t.Fatalf("run init exit code = %d, stderr=%q", code, string(stderrBytes))
	}
	if !strings.Contains(string(stdoutBytes), "Init") || !strings.Contains(string(stdoutBytes), "CLAW.md") {
		t.Fatalf("expected init report in stdout, got %q", string(stdoutBytes))
	}
}

func TestChatInitCommandCreatesScaffold(t *testing.T) {
	dir := t.TempDir()
	r := newScriptedRuntime(t, dir, "init-chat", &scriptedChatClient{})

	result := r.executeCommand("/init")
	if !result.handled {
		t.Fatalf("/init was not handled")
	}
	if result.exitCode != -1 {
		t.Fatalf("unexpected exit code: %d", result.exitCode)
	}
	if !strings.Contains(result.output, "CLAW.md") {
		t.Fatalf("expected init output to mention CLAW.md, got %q", result.output)
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAW.md")); err != nil {
		t.Fatalf("expected CLAW.md to be created: %v", err)
	}
}

func TestPlanCommandPrefersRootPlanFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("root plan"), 0o644); err != nil {
		t.Fatalf("write root plan: %v", err)
	}

	r := &chatRuntime{cfg: configForPlanTest(dir)}
	if got := r.planCommand(); got != "root plan" {
		t.Fatalf("unexpected plan content: got %q want %q", got, "root plan")
	}
}

func configForPlanTest(dir string) config.Config {
	return config.Config{WorkDir: dir}
}
