package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiny-agent-cli/internal/config"
	"tiny-agent-cli/internal/session"
	"tiny-agent-cli/internal/tools"
)

func TestReadPlanFileFallsBackToDocs(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "plan.md"), []byte("docs fallback"), 0o644); err != nil {
		t.Fatalf("write docs plan: %v", err)
	}

	path, text, err := readPlanFile(dir)
	if err != nil {
		t.Fatalf("readPlanFile returned error: %v", err)
	}
	if path != filepath.Join(dir, "docs", "plan.md") {
		t.Fatalf("unexpected plan path: %q", path)
	}
	if text != "docs fallback" {
		t.Fatalf("unexpected plan text: %q", text)
	}
}

func TestRunPlanPrintsWorkspacePlan(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("hello plan"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

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

	code := runPlan([]string{"--workdir", dir})
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
		t.Fatalf("runPlan exit code = %d, stderr=%q", code, string(stderrBytes))
	}
	if strings.TrimSpace(string(stdoutBytes)) != "hello plan" {
		t.Fatalf("unexpected plan output: %q", string(stdoutBytes))
	}
}

func TestRunStatusPrintsWorkspaceState(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".tacli")
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("status plan"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".agents", "skills", "demo-skill"), 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".agents", "skills", "demo-skill", "SKILL.md"), []byte("# Demo Skill\nDemo description"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	if err := session.Save(session.SessionPath(stateDir, "demo"), session.State{SessionName: "demo"}); err != nil {
		t.Fatalf("save session: %v", err)
	}
	store, err := tools.LoadPermissionStore(tools.PermissionPath(stateDir))
	if err != nil {
		t.Fatalf("load permission store: %v", err)
	}
	store.SetCommandMode("git status *", tools.PermissionModeAllow)
	if err := store.Save(); err != nil {
		t.Fatalf("save permission store: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(tools.AuditPath(stateDir)), 0o755); err != nil {
		t.Fatalf("mkdir audit dir: %v", err)
	}
	if err := os.WriteFile(tools.AuditPath(stateDir), []byte("[]\n"), 0o644); err != nil {
		t.Fatalf("write audit file: %v", err)
	}

	output := renderWorkspaceStatus(testWorkspaceConfig(dir, stateDir))
	for _, want := range []string{
		"workdir=" + dir,
		"state=" + stateDir,
		"plan=" + filepath.Join(dir, "plan.md"),
		"instructions=0",
		"sessions=1",
		"command_rules=1",
		"skills=",
		"capabilities=",
		"policy=" + tools.PermissionPath(stateDir),
		"audit=" + tools.AuditPath(stateDir),
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected status output to contain %q, got %q", want, output)
		}
	}
}

func TestRunCapabilitiesListsPacks(t *testing.T) {
	dir := t.TempDir()
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

	code := runCapabilities([]string{"--workdir", dir, "repo-research"})
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
		t.Fatalf("runCapabilities exit code = %d, stderr=%q", code, string(stderrBytes))
	}
	text := string(stdoutBytes)
	if !strings.Contains(text, "repo-research:") || !strings.Contains(text, "roles: explore, plan") {
		t.Fatalf("expected repo-research pack, got %q", text)
	}
}

func TestRunSkillsListsWorkspaceSkill(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".agents", "skills", "demo-skill"), 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".agents", "skills", "demo-skill", "SKILL.md"), []byte("# Demo Skill\nDemo description\nTools: read_file, write_file"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

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

	code := runSkills([]string{"--workdir", dir})
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
		t.Fatalf("runSkills exit code = %d, stderr=%q", code, string(stderrBytes))
	}
	text := string(stdoutBytes)
	if !strings.Contains(text, "Demo Skill [local]: Demo description tools=read_file,write_file") {
		t.Fatalf("expected workspace skill in output, got %q", text)
	}
}

func testWorkspaceConfig(workDir, stateDir string) config.Config {
	return config.Config{
		WorkDir:      workDir,
		StateDir:     stateDir,
		ApprovalMode: tools.PermissionModePrompt,
		Model:        "test-model",
		BaseURL:      "http://example.test/v1",
	}
}
