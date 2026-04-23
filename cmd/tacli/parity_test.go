package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"tiny-agent-cli/internal/model"
)

type cliHarnessResult struct {
	code   int
	stdout string
	stderr string
}

func runCLIHarness(t *testing.T, workDir string, args []string, stdin []byte) cliHarnessResult {
	t.Helper()

	stateDir := filepath.Join(workDir, ".tacli")
	oldWorkDir, hadWorkDir := os.LookupEnv("AGENT_WORKDIR")
	oldStateDir, hadStateDir := os.LookupEnv("AGENT_STATE_DIR")
	oldStdin, oldStdout, oldStderr := os.Stdin, os.Stdout, os.Stderr

	if err := os.Setenv("AGENT_WORKDIR", workDir); err != nil {
		t.Fatalf("set AGENT_WORKDIR: %v", err)
	}
	if err := os.Setenv("AGENT_STATE_DIR", stateDir); err != nil {
		t.Fatalf("set AGENT_STATE_DIR: %v", err)
	}
	defer func() {
		restoreEnv("AGENT_WORKDIR", oldWorkDir, hadWorkDir)
		restoreEnv("AGENT_STATE_DIR", oldStateDir, hadStateDir)
		os.Stdin, os.Stdout, os.Stderr = oldStdin, oldStdout, oldStderr
	}()

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

func restoreEnv(key, oldValue string, hadValue bool) {
	if hadValue {
		_ = os.Setenv(key, oldValue)
		return
	}
	_ = os.Unsetenv(key)
}

type mockModelStep struct {
	status     int
	retryAfter string
	response   model.Response
	body       string
}

type mockModelServer struct {
	server   *httptest.Server
	mu       sync.Mutex
	steps    []mockModelStep
	requests []model.Request
}

func newMockModelServer(t *testing.T, steps ...mockModelStep) *mockModelServer {
	t.Helper()
	mock := &mockModelServer{steps: steps}
	mock.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || (r.URL.Path != "/chat/completions" && r.URL.Path != "/v1/chat/completions") {
			http.NotFound(w, r)
			return
		}
		var req model.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		mock.mu.Lock()
		mock.requests = append(mock.requests, req)
		index := len(mock.requests) - 1
		step := mockModelStep{status: http.StatusOK, response: assistantContentResponse("ok")}
		if index < len(mock.steps) {
			step = mock.steps[index]
		}
		mock.mu.Unlock()

		if step.status == 0 {
			step.status = http.StatusOK
		}
		if step.retryAfter != "" {
			w.Header().Set("Retry-After", step.retryAfter)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(step.status)
		if step.body != "" {
			_, _ = w.Write([]byte(step.body))
			return
		}
		if err := json.NewEncoder(w).Encode(step.response); err != nil {
			t.Errorf("encode mock response: %v", err)
		}
	}))
	t.Cleanup(mock.server.Close)
	return mock
}

func (m *mockModelServer) URL() string {
	return m.server.URL
}

func (m *mockModelServer) RequestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

func assistantContentResponse(content string) model.Response {
	return model.Response{
		Choices: []model.Choice{{
			Message: model.Message{
				Role:    "assistant",
				Content: content,
			},
		}},
	}
}

func assistantToolCallResponse(calls ...model.ToolCall) model.Response {
	return model.Response{
		Choices: []model.Choice{{
			Message: model.Message{
				Role:      "assistant",
				ToolCalls: calls,
			},
		}},
	}
}

func runCommandToolCall(id, command string) model.ToolCall {
	args, _ := json.Marshal(map[string]string{"command": command})
	return model.ToolCall{
		ID:   id,
		Type: "function",
		Function: model.ToolFunction{
			Name:      "run_command",
			Arguments: string(args),
		},
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
	if result.code != 0 || !strings.Contains(result.stdout, "Demo [local] enabled: Parity scenario tools=read_file") {
		t.Fatalf("skills failed: %#v", result)
	}

	result = runCLIHarness(t, workDir, []string{"capabilities", "--workdir", workDir, "repo-research"}, nil)
	if result.code != 0 || !strings.Contains(result.stdout, "repo-research:") {
		t.Fatalf("capabilities failed: %#v", result)
	}

	result = runCLIHarness(t, workDir, []string{"status", "--workdir", workDir}, nil)
	if result.code != 0 {
		t.Fatalf("status failed: %#v", result)
	}
	for _, want := range []string{
		"plan=" + filepath.Join(workDir, "plan.md"),
		"skills=",
		"capabilities=",
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
		"--resume", "policy-parity",
	}, stdin)
	if result.code != 0 {
		t.Fatalf("chat policy scenario failed: %#v", result)
	}
	for _, want := range []string{
		"1. deny git push *",
		"command_rules=1",
		"conversation=policy-parity",
	} {
		if !strings.Contains(result.stdout, want) {
			t.Fatalf("expected stdout to contain %q, got %q", want, result.stdout)
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
		"--resume", "resume-parity",
	}, []byte("/memory remember alpha\x00/save\n"))
	if result.code != 0 {
		t.Fatalf("first chat run failed: %#v", result)
	}

	result = runCLIHarness(t, workDir, []string{
		"chat",
		"--workdir", workDir,
		"--base-url", "http://example.test/v1",
		"--model", "test-model",
		"--api-key", "test-key",
		"--resume", "resume-parity",
	}, []byte("/memory show\n"))
	if result.code != 0 {
		t.Fatalf("second chat run failed: %#v", result)
	}
	if !strings.Contains(result.stdout, "alpha") || !strings.Contains(result.stdout, "project_notes=1") {
		t.Fatalf("expected resumed memory note, got %#v", result)
	}
}

func TestCLIParityRepeatedToolFailureScenario(t *testing.T) {
	workDir := t.TempDir()
	mock := newMockModelServer(t, mockModelStep{
		response: assistantToolCallResponse(
			runCommandToolCall("call-1", "false"),
			runCommandToolCall("call-2", "false"),
			runCommandToolCall("call-3", "false"),
		),
	})

	result := runCLIHarness(t, workDir, []string{
		"run",
		"--dangerously",
		"--base-url", mock.URL(),
		"--model", "test-model",
		"--api-key", "test-key",
		"--workdir", workDir,
		"Try the failing install commands even if they keep failing.",
	}, nil)
	if result.code != 0 {
		t.Fatalf("run failed: %#v", result)
	}
	if !strings.Contains(result.stdout, "repeated tool failures") {
		t.Fatalf("expected repeated failure stop in stdout, got %#v", result)
	}
	if mock.RequestCount() != 1 {
		t.Fatalf("expected one model request before repeated-failure stop, got %d", mock.RequestCount())
	}
}

func TestCLIParityEmptyPostToolAnswerFallbackScenario(t *testing.T) {
	workDir := t.TempDir()
	mock := newMockModelServer(t,
		mockModelStep{response: assistantToolCallResponse(runCommandToolCall("call-1", "printf fallback-output"))},
		mockModelStep{response: model.Response{Choices: []model.Choice{{Message: model.Message{Role: "assistant"}}}}},
		mockModelStep{response: model.Response{Choices: []model.Choice{{Message: model.Message{Role: "assistant"}}}}},
	)

	result := runCLIHarness(t, workDir, []string{
		"run",
		"--dangerously",
		"--base-url", mock.URL(),
		"--model", "test-model",
		"--api-key", "test-key",
		"--workdir", workDir,
		"Run the command and report the output.",
	}, nil)
	if result.code != 0 {
		t.Fatalf("run failed: %#v", result)
	}
	if strings.TrimSpace(result.stdout) != "fallback-output" {
		t.Fatalf("expected fallback tool output, got %#v", result)
	}
	if mock.RequestCount() != 3 {
		t.Fatalf("expected retry plus fallback requests, got %d", mock.RequestCount())
	}
}

func TestCLIParityRateLimitRetryScenario(t *testing.T) {
	workDir := t.TempDir()
	mock := newMockModelServer(t,
		mockModelStep{
			status:     http.StatusTooManyRequests,
			retryAfter: "0",
			body:       `{"error":"rate limit"}`,
		},
		mockModelStep{response: assistantContentResponse("retry-ok")},
	)

	result := runCLIHarness(t, workDir, []string{
		"run",
		"--base-url", mock.URL(),
		"--model", "test-model",
		"--api-key", "test-key",
		"--workdir", workDir,
		"Reply with retry-ok.",
	}, nil)
	if result.code != 0 {
		t.Fatalf("run failed after rate-limit retry: %#v", result)
	}
	if strings.TrimSpace(result.stdout) != "retry-ok" {
		t.Fatalf("unexpected stdout after retry: %#v", result)
	}
	if mock.RequestCount() != 2 {
		t.Fatalf("expected one retry after 429, got %d requests", mock.RequestCount())
	}
}
