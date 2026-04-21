package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"tiny-agent-cli/internal/agent"
	"tiny-agent-cli/internal/config"
	"tiny-agent-cli/internal/i18n"
	"tiny-agent-cli/internal/mcp"
	"tiny-agent-cli/internal/memory"
	"tiny-agent-cli/internal/model"
	"tiny-agent-cli/internal/plugins"
	"tiny-agent-cli/internal/session"
	"tiny-agent-cli/internal/tools"
	"tiny-agent-cli/internal/transport"
)

func TestPrintUsageIncludesVersion(t *testing.T) {
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() {
		os.Stderr = old
	})

	printUsage()
	_ = w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read usage: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "tacli "+version) {
		t.Fatalf("expected usage to include explicit version, got %q", got)
	}
}

func TestExtractStableMemoryNotes(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "My preference is short Chinese answers.\nThis repo uses Go."},
		{Role: "assistant", Content: "I will keep that in mind."},
		{Role: "user", Content: "Please debug this failing test."},
	}

	got := extractStableMemoryNotes(messages)
	want := []string{
		"Prefer short Chinese answers",
		"This repo uses Go",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected notes: %#v", got)
	}
}

func TestExtractStableMemoryNotesSkipsTransientRequests(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: "Please inspect this bug and show me the stack trace."},
		{Role: "user", Content: "Summarize the current logs in plain text."},
	}

	got := extractStableMemoryNotes(messages)
	if len(got) != 0 {
		t.Fatalf("expected no notes, got %#v", got)
	}
}

func TestBuildConversationSummaryInputKeepsRecentMessages(t *testing.T) {
	var messages []model.Message
	for i := 0; i < memorySummaryMaxMessages+6; i++ {
		messages = append(messages, model.Message{
			Role:    "user",
			Content: fmt.Sprintf("message-%02d", i),
		})
	}

	got := buildConversationSummaryInput(messages)
	if strings.Contains(got, "message-00") {
		t.Fatalf("expected oldest message to be trimmed: %q", got)
	}
	if !strings.Contains(got, fmt.Sprintf("message-%02d", memorySummaryMaxMessages+5)) {
		t.Fatalf("expected newest message to be kept: %q", got)
	}
	if count := strings.Count(got, "user: message-"); count != memorySummaryMaxMessages {
		t.Fatalf("expected %d recent messages, got %d", memorySummaryMaxMessages, count)
	}
}

func TestBuildConversationSummaryInputTruncatesLargeEntries(t *testing.T) {
	messages := []model.Message{
		{Role: "user", Content: strings.Repeat("alpha ", 120)},
		{Role: "assistant", Content: strings.Repeat("beta ", 120)},
	}

	got := buildConversationSummaryInput(messages)
	if len(got) >= len(model.ContentString(messages[0].Content))+len(model.ContentString(messages[1].Content)) {
		t.Fatalf("expected summary input to truncate large entries, got length %d", len(got))
	}
	if !strings.Contains(got, "user: alpha") {
		t.Fatalf("expected user entry to remain recognizable, got %q", got)
	}
	if !strings.Contains(got, "assistant: beta") {
		t.Fatalf("expected assistant entry to remain recognizable, got %q", got)
	}
}

func TestPeelGlobalDangerously(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		want   []string
		danger bool
	}{
		{name: "short flag", args: []string{"-d", "inspect repo"}, want: []string{"inspect repo"}, danger: true},
		{name: "long flag", args: []string{"--dangerously", "run", "go test"}, want: []string{"run", "go test"}, danger: true},
		{name: "single dash long flag", args: []string{"-dangerously", "chat"}, want: []string{"chat"}, danger: true},
		{name: "no flag", args: []string{"chat"}, want: []string{"chat"}, danger: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotArgs, gotDanger := peelGlobalDangerously(tt.args)
			if gotDanger != tt.danger {
				t.Fatalf("danger mismatch: got %v want %v", gotDanger, tt.danger)
			}
			if !reflect.DeepEqual(gotArgs, tt.want) {
				t.Fatalf("args mismatch: got %#v want %#v", gotArgs, tt.want)
			}
		})
	}
}

func TestWithDangerouslyFlag(t *testing.T) {
	got := withDangerouslyFlag([]string{"chat"}, true)
	want := []string{"--dangerously", "chat"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected args: %#v", got)
	}
}

func TestResolveChatSessionName(t *testing.T) {
	now := time.Date(2026, time.March, 20, 14, 5, 6, 0, time.FixedZone("CST", 8*3600))
	defaultName := defaultChatSessionName(now)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "blank creates new session", input: "", want: defaultName},
		{name: "new creates new session", input: "new", want: defaultName},
		{name: "explicit session kept", input: "bugfix", want: "bugfix"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveChatSessionName(tt.input, now); got != tt.want {
				t.Fatalf("unexpected session: got %q want %q", got, tt.want)
			}
		})
	}
}

func TestRemoteOwnerFromURLParsesGitHubURL(t *testing.T) {
	if got := remoteOwnerFromURL("git@github.com:acme/tiny-agent-cli.git"); got != "acme" {
		t.Fatalf("unexpected owner: %q", got)
	}
}

func TestTeamMemoryCommandRememberAndForget(t *testing.T) {
	r := &chatRuntime{
		cfg:        config.Config{Model: "test-model"},
		memoryPath: filepath.Join(t.TempDir(), "memory.json"),
		teamKey:    "eng",
		scopeKey:   "/repo",
		session:    agent.New(chatClientStub{}, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil).NewSession(),
	}
	output := r.teamMemoryCommand([]string{"remember", "Run", "reviews"}, "/memory team remember Run reviews")
	if !strings.Contains(output, "team_notes=1") {
		t.Fatalf("expected team note in output, got %q", output)
	}
	if len(r.teamMemory) != 1 || r.teamMemory[0] != "Run reviews" {
		t.Fatalf("unexpected team memory: %#v", r.teamMemory)
	}

	output = r.teamMemoryCommand([]string{"forget", "reviews"}, "/memory team forget reviews")
	if !strings.Contains(output, "removed 1 team memory note") {
		t.Fatalf("unexpected forget output: %q", output)
	}
	if len(r.teamMemory) != 0 {
		t.Fatalf("expected team memory to be cleared, got %#v", r.teamMemory)
	}
}

func TestPolicyCommandUpdatesAndPersistsStore(t *testing.T) {
	stateDir := t.TempDir()
	r := &chatRuntime{
		cfg:            config.Config{StateDir: stateDir, WorkDir: stateDir, Model: "test-model", ApprovalMode: "confirm"},
		permissionPath: tools.PermissionPath(stateDir),
		permissions:    loadRuntimePolicy(config.Config{StateDir: stateDir}),
		approver:       newStubApprover(),
		session:        agent.New(chatClientStub{}, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil).NewSession(),
	}

	output := r.policyCommand([]string{"/policy", "default", "allow"}, "/policy default allow")
	if !strings.Contains(output, "default=allow") {
		t.Fatalf("unexpected default policy output: %q", output)
	}

	output = r.policyCommand([]string{"/policy", "tool", "write_file", "deny"}, "/policy tool write_file deny")
	if !strings.Contains(output, "write_file=deny") {
		t.Fatalf("unexpected tool policy output: %q", output)
	}

	reloaded, err := tools.LoadPermissionStore(r.permissionPath)
	if err != nil {
		t.Fatalf("reload permission store: %v", err)
	}
	if reloaded.ModeForTool("write_file") != tools.PermissionModeDeny {
		t.Fatalf("expected persisted deny mode, got %#v", reloaded.Snapshot())
	}
}

func TestPolicyCommandManagesCommandRules(t *testing.T) {
	stateDir := t.TempDir()
	r := &chatRuntime{
		cfg:            config.Config{StateDir: stateDir, WorkDir: stateDir, Model: "test-model", ApprovalMode: "confirm"},
		permissionPath: tools.PermissionPath(stateDir),
		permissions:    loadRuntimePolicy(config.Config{StateDir: stateDir}),
		approver:       newStubApprover(),
		session:        agent.New(chatClientStub{}, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil).NewSession(),
	}

	output := r.policyCommand([]string{"/policy", "command", "add", "allow", "git", "status", "*"}, "/policy command add allow git status *")
	if !strings.Contains(output, "1. allow git status *") {
		t.Fatalf("unexpected command policy output: %q", output)
	}

	reloaded, err := tools.LoadPermissionStore(r.permissionPath)
	if err != nil {
		t.Fatalf("reload permission store: %v", err)
	}
	rule, ok := reloaded.MatchCommandRule("git status --short")
	if !ok || rule.Mode != tools.PermissionModeAllow {
		t.Fatalf("expected persisted command rule, got %#v ok=%t", rule, ok)
	}

	output = r.policyCommand([]string{"/policy", "command", "remove", "1"}, "/policy command remove 1")
	if strings.Contains(output, "1. allow git status *") {
		t.Fatalf("expected rule to be removed, got %q", output)
	}
}

func TestCapabilitiesCommandShowsNamedPack(t *testing.T) {
	r := &chatRuntime{}
	result := r.capabilitiesCommand([]string{"/capabilities", "release"})
	if !strings.Contains(result, "release:") || !strings.Contains(result, "tools:") {
		t.Fatalf("unexpected capabilities output: %q", result)
	}
}

type stubApprover struct{}

func newStubApprover() stubApprover {
	return stubApprover{}
}

func (stubApprover) Mode() string { return tools.ApprovalConfirm }

func (stubApprover) SetMode(string) error { return nil }

func (stubApprover) ApproveCommand(context.Context, string) (bool, error) { return true, nil }

func (stubApprover) ApproveWrite(context.Context, string, string) (bool, error) { return true, nil }

func TestStartupMode(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		interactive bool
		want        string
	}{
		{name: "interactive default", args: nil, interactive: true, want: "chat"},
		{name: "non interactive default", args: nil, interactive: false, want: "run"},
		{name: "explicit chat", args: []string{"chat"}, interactive: true, want: "chat"},
		{name: "explicit run", args: []string{"run", "inspect repo"}, interactive: true, want: "run"},
		{name: "task shorthand", args: []string{"inspect repo"}, interactive: true, want: "run"},
		{name: "global dangerously chat", args: []string{"-d", "chat"}, interactive: true, want: "chat"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := startupMode(tt.args, tt.interactive); got != tt.want {
				t.Fatalf("unexpected mode: got %q want %q", got, tt.want)
			}
		})
	}
}

func TestIsRunShorthand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "quoted single task", args: []string{"inspect repo"}, want: true},
		{name: "single token command-like", args: []string{"status"}, want: false},
		{name: "multi token cli args", args: []string{"inspect", "repo"}, want: false},
		{name: "empty args", args: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRunShorthand(tt.args); got != tt.want {
				t.Fatalf("isRunShorthand(%v)=%v want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestRunUnknownCommandPrintsUsageAndReturnsTwo(t *testing.T) {
	oldStderr := os.Stderr
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	defer stderrR.Close()
	os.Stderr = stderrW
	defer func() {
		os.Stderr = oldStderr
	}()

	code := run([]string{"definitely-unknown-subcmd"})
	_ = stderrW.Close()
	stderrBytes, err := io.ReadAll(stderrR)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	stderrText := string(stderrBytes)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d stderr=%q", code, stderrText)
	}
	if !strings.Contains(stderrText, "unknown command: definitely-unknown-subcmd") {
		t.Fatalf("missing unknown command message: %q", stderrText)
	}
	if !strings.Contains(stderrText, "Usage:") {
		t.Fatalf("missing usage in stderr: %q", stderrText)
	}
}

func TestParseAgentFlagsValidatesOutputMode(t *testing.T) {
	_, opts, _, _, err := parseAgentFlags("run", []string{"--output", "jsonl", "inspect repo"})
	if err != nil {
		t.Fatalf("parseAgentFlags returned error: %v", err)
	}
	if opts.outputMode != transport.OutputJSONL {
		t.Fatalf("unexpected output mode: %q", opts.outputMode)
	}
}

func TestParseAgentFlagsRejectsStructuredChatOutput(t *testing.T) {
	_, _, _, _, err := parseAgentFlags("chat", []string{"--output", "json"})
	if err == nil {
		t.Fatalf("expected error for structured chat output")
	}
}

func TestParseAgentFlagsStateDirFollowsWorkDirByDefault(t *testing.T) {
	t.Setenv("AGENT_STATE_DIR", "")
	dir := t.TempDir()

	cfg, _, _, _, err := parseAgentFlags("chat", []string{
		"--workdir", dir,
		"--base-url", "http://example.test/v1",
		"--model", "test-model",
	})
	if err != nil {
		t.Fatalf("parseAgentFlags returned error: %v", err)
	}

	want := filepath.Join(dir, ".tacli")
	if cfg.StateDir != want {
		t.Fatalf("unexpected state dir: got %q want %q", cfg.StateDir, want)
	}
}

func TestParseAgentFlagsPreservesExplicitStateDir(t *testing.T) {
	stateDir := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("AGENT_STATE_DIR", stateDir)

	cfg, _, _, _, err := parseAgentFlags("chat", []string{
		"--workdir", workDir,
		"--base-url", "http://example.test/v1",
		"--model", "test-model",
	})
	if err != nil {
		t.Fatalf("parseAgentFlags returned error: %v", err)
	}

	if cfg.StateDir != stateDir {
		t.Fatalf("unexpected state dir: got %q want %q", cfg.StateDir, stateDir)
	}
}

func TestShouldPromptForLanguage(t *testing.T) {
	if !shouldPromptForLanguage("chat", true) {
		t.Fatalf("expected interactive chat to prompt for language")
	}
	if shouldPromptForLanguage("run", true) {
		t.Fatalf("expected run mode not to prompt for language")
	}
	if shouldPromptForLanguage("chat", false) {
		t.Fatalf("expected non-interactive chat not to prompt for language")
	}
}

func TestReadNonInteractiveTasksTreatsMultilineInputAsSingleTask(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("first line\nsecond line\nthird line\n"))
	tasks, err := readNonInteractiveTasks(reader)
	if err != nil {
		t.Fatalf("readNonInteractiveTasks returned error: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one task, got %d: %#v", len(tasks), tasks)
	}
	if tasks[0] != "first line\nsecond line\nthird line" {
		t.Fatalf("unexpected task content: %q", tasks[0])
	}
}

func TestReadNonInteractiveTasksSupportsNulSeparatedBatches(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("first task\x00second task\x00\n\x00third task"))
	tasks, err := readNonInteractiveTasks(reader)
	if err != nil {
		t.Fatalf("readNonInteractiveTasks returned error: %v", err)
	}
	want := []string{"first task", "second task", "third task"}
	if len(tasks) != len(want) {
		t.Fatalf("unexpected task count: got %d want %d (%#v)", len(tasks), len(want), tasks)
	}
	for i := range want {
		if tasks[i] != want[i] {
			t.Fatalf("task %d mismatch: got %q want %q", i, tasks[i], want[i])
		}
	}
}

func TestDefaultStartupLanguage(t *testing.T) {
	oldLCAll := os.Getenv("LC_ALL")
	oldLCMessages := os.Getenv("LC_MESSAGES")
	oldLang := os.Getenv("LANG")
	t.Cleanup(func() {
		_ = os.Setenv("LC_ALL", oldLCAll)
		_ = os.Setenv("LC_MESSAGES", oldLCMessages)
		_ = os.Setenv("LANG", oldLang)
	})

	_ = os.Setenv("LC_ALL", "")
	_ = os.Setenv("LC_MESSAGES", "")
	_ = os.Setenv("LANG", "zh_CN.UTF-8")
	if got := defaultStartupLanguage(); got != i18n.LangCN {
		t.Fatalf("expected Chinese from LANG, got %q", got)
	}

	_ = os.Setenv("LANG", "en_US.UTF-8")
	if got := defaultStartupLanguage(); got != i18n.LangEN {
		t.Fatalf("expected English from LANG, got %q", got)
	}
}

func TestUseNativeChatInputModeTracksFullscreenFlag(t *testing.T) {
	t.Setenv("TACLI_FULLSCREEN", "1")
	if useNativeChatInputMode() {
		t.Fatalf("expected native mode off when fullscreen is enabled")
	}
	t.Setenv("TACLI_FULLSCREEN", "0")
	if !useNativeChatInputMode() {
		t.Fatalf("expected native mode on when fullscreen is disabled")
	}
}

func TestFormatJobList(t *testing.T) {
	text := formatJobList([]jobSnapshot{{
		ID:         "job-001",
		Status:     jobReady,
		Model:      "test-model",
		TaskCount:  2,
		Queued:     1,
		LastPrompt: "inspect the repo deeply",
		LastOutput: "done",
	}})
	if !strings.Contains(text, "job-001  ready  tasks=2") {
		t.Fatalf("missing job header: %q", text)
	}
	if !strings.Contains(text, "queued=1") {
		t.Fatalf("missing queued count: %q", text)
	}
	if !strings.Contains(text, "last_prompt: inspect the repo deeply") {
		t.Fatalf("missing prompt: %q", text)
	}
}

func TestCompactJobTextSingleLine(t *testing.T) {
	got := compactJobText("alpha\n beta   gamma", 0)
	if got != "alpha beta gamma" {
		t.Fatalf("unexpected compact text: %q", got)
	}
}

func TestSummarizeJobForSession(t *testing.T) {
	text := summarizeJobForSession(jobSnapshot{
		ID:         "job-002",
		Status:     jobReady,
		LastPrompt: "inspect config loading",
		LastOutput: "Key findings:\n- found two issues\nRelevant files:\n- internal/config/config.go\nRisks or unknowns:\n- env fallback may mask errors\nRecommended next steps:\n- patch validation",
		Summary: jobSummary{
			Findings:  []string{"found two issues"},
			Files:     []string{"internal/config/config.go"},
			Risks:     []string{"env fallback may mask errors"},
			NextSteps: []string{"patch validation"},
		},
	})
	if !strings.Contains(text, "[background job job-002]") {
		t.Fatalf("missing job id: %q", text)
	}
	if !strings.Contains(text, "findings:") || !strings.Contains(text, "files:") {
		t.Fatalf("missing structured summary: %q", text)
	}
}

func TestSavePersistsSessionState(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{
		Model:          "test-model",
		BaseURL:        "http://127.0.0.1:11434/v1",
		WorkDir:        dir,
		StateDir:       dir,
		ContextWindow:  32768,
		MaxSteps:       4,
		CommandTimeout: time.Second,
		Shell:          "bash",
		ApprovalMode:   tools.ApprovalConfirm,
	}
	r := &chatRuntime{
		cfg:           cfg,
		approver:      tools.NewTerminalApprover(bufio.NewReader(strings.NewReader("")), os.Stderr, tools.ApprovalDangerously, true),
		session:       agentSessionStub(),
		sessionName:   "chat-test",
		statePath:     session.SessionPath(dir, "chat-test"),
		outputMode:    "terminal",
		scopeKey:      memory.ScopeKey(dir),
		globalMemory:  []string{"Prefer concise answers"},
		projectMemory: []string{"Repo uses Go"},
	}
	if err := r.save(); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	state, err := session.Load(r.statePath)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if state.ApprovalMode != tools.ApprovalConfirm {
		t.Fatalf("approval mode mismatch: %q", state.ApprovalMode)
	}
	if state.Model != cfg.Model {
		t.Fatalf("model mismatch: %q", state.Model)
	}
	if state.OutputMode != "terminal" {
		t.Fatalf("output mode mismatch: %q", state.OutputMode)
	}
	if state.ScopeKey != r.scopeKey {
		t.Fatalf("scope key mismatch: %q", state.ScopeKey)
	}
	if len(state.GlobalMemory) != 1 || state.GlobalMemory[0] != r.globalMemory[0] {
		t.Fatalf("global memory mismatch: %#v", state.GlobalMemory)
	}
	if len(state.ProjectMemory) != 1 || state.ProjectMemory[0] != r.projectMemory[0] {
		t.Fatalf("project memory mismatch: %#v", state.ProjectMemory)
	}
}

func agentSessionStub() *agent.Session {
	a := agent.New(chatClientStub{}, tools.NewRegistry(".", "bash", time.Second, nil), 32768, nil)
	return a.NewSession()
}

type chatClientStub struct{}

func (chatClientStub) Complete(_ context.Context, _ model.Request) (model.Response, error) {
	return model.Response{}, nil
}

type scriptedChatClient struct {
	responses []model.Response
	requests  int
	log       []model.Request
}

func (s *scriptedChatClient) Complete(_ context.Context, req model.Request) (model.Response, error) {
	s.log = append(s.log, req)
	if s.requests >= len(s.responses) {
		return model.Response{}, nil
	}
	resp := s.responses[s.requests]
	s.requests++
	return resp, nil
}

func TestOutputCommandDeprecated(t *testing.T) {
	r := &chatRuntime{outputMode: "terminal"}
	result := r.executeCommand("/output raw")
	if !result.handled {
		t.Fatalf("expected /output to be handled")
	}
	if r.outputMode != "terminal" {
		t.Fatalf("output mode should remain unchanged, got %q", r.outputMode)
	}
	if !strings.Contains(result.output, "deprecated") {
		t.Fatalf("expected deprecation message, got %q", result.output)
	}
}

func TestParseModelCommand(t *testing.T) {
	name, verify, err := parseModelCommand([]string{"/model", "gpt-5.4-mini"})
	if err != nil {
		t.Fatalf("parseModelCommand returned error: %v", err)
	}
	if name != "gpt-5.4-mini" || !verify {
		t.Fatalf("unexpected parse result: name=%q verify=%v", name, verify)
	}

	name, verify, err = parseModelCommand([]string{"/model", "--no-verify", "custom-model"})
	if err != nil {
		t.Fatalf("parseModelCommand returned error: %v", err)
	}
	if name != "custom-model" || verify {
		t.Fatalf("unexpected no-verify parse result: name=%q verify=%v", name, verify)
	}
}

func TestModelCommandRejectsUnknownModelWhenVerified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" && r.URL.Path != "/api/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"known-model"}]}`)
	}))
	defer server.Close()

	r := newMemoryTestRuntime(t)
	original := r.cfg.Model
	r.cfg.BaseURL = server.URL + "/v1"
	r.cfg.APIKey = "test-key"

	result := r.executeCommand("/model missing-model")
	if !result.handled {
		t.Fatalf("expected /model command to be handled")
	}
	if !strings.Contains(result.output, "was not found on the current endpoint") {
		t.Fatalf("expected not-found output, got %q", result.output)
	}
	if r.cfg.Model != original {
		t.Fatalf("model should remain unchanged on failed verification: got %q want %q", r.cfg.Model, original)
	}
}

func TestModelCommandCanSkipVerification(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" && r.URL.Path != "/api/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"known-model"}]}`)
	}))
	defer server.Close()

	r := newMemoryTestRuntime(t)
	r.cfg.BaseURL = server.URL + "/v1"
	r.cfg.APIKey = "test-key"

	result := r.executeCommand("/model --no-verify missing-model")
	if !result.handled {
		t.Fatalf("expected /model command to be handled")
	}
	if !strings.Contains(result.output, "model set to missing-model") {
		t.Fatalf("expected model-set output, got %q", result.output)
	}
	if r.cfg.Model != "missing-model" {
		t.Fatalf("model should update when verification is disabled, got %q", r.cfg.Model)
	}
}

func TestParseReviewArgs(t *testing.T) {
	opts, err := parseReviewArgs([]string{"main", "feature", "--staged", "--path", "internal/agent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.base != "main" || opts.target != "feature" || !opts.staged || opts.path != "internal/agent" {
		t.Fatalf("unexpected review opts: %#v", opts)
	}
}

func TestBuildReviewPromptIncludesScope(t *testing.T) {
	prompt := buildReviewPrompt(reviewOptions{
		base:   "main",
		target: "feature",
		path:   "internal/agent",
		staged: true,
	}, reviewPreflight{
		changedFiles: []string{"internal/agent/agent.go", "internal/agent/agent_test.go"},
		diffStat:     "2 files changed, 10 insertions(+)",
		docsOnly:     false,
	})
	if !strings.Contains(prompt, "for staged changes") {
		t.Fatalf("expected staged scope, got %q", prompt)
	}
	if !strings.Contains(prompt, "against main") || !strings.Contains(prompt, "to feature") {
		t.Fatalf("expected base/target scope, got %q", prompt)
	}
	if !strings.Contains(prompt, "scoped to internal/agent") {
		t.Fatalf("expected path scope, got %q", prompt)
	}
	if !strings.Contains(prompt, "changed_files(2)=") || !strings.Contains(prompt, "diff_stat=2 files changed") {
		t.Fatalf("expected preflight facts, got %q", prompt)
	}
}

func TestOnlyDocLikeFiles(t *testing.T) {
	if !onlyDocLikeFiles([]string{"README.md", "docs/usage.txt", "assets/logo.svg"}) {
		t.Fatalf("expected doc-like files to be true")
	}
	if onlyDocLikeFiles([]string{"README.md", "cmd/tacli/main.go"}) {
		t.Fatalf("expected mixed code/doc files to be false")
	}
}

func TestAuditCommandStatsAndTail(t *testing.T) {
	r := newMemoryTestRuntime(t)
	r.auditPath = tools.AuditPath(r.cfg.StateDir)
	sink := tools.NewFileAuditSink(r.auditPath)
	sink.RecordToolEvent(context.Background(), tools.ToolAuditEvent{
		Time:   time.Now().Add(-1 * time.Second),
		Tool:   "read_file",
		Status: "ok",
	})
	sink.RecordToolEvent(context.Background(), tools.ToolAuditEvent{
		Time:   time.Now(),
		Tool:   "run_command",
		Status: "error",
		Error:  "command failed",
	})

	stats := r.executeCommand("/audit stats")
	if !stats.handled {
		t.Fatalf("expected /audit stats handled")
	}
	if !strings.Contains(stats.output, "audit=") {
		t.Fatalf("unexpected /audit stats output: %q", stats.output)
	}

	tail := r.executeCommand("/audit tail 1")
	if !tail.handled {
		t.Fatalf("expected /audit tail handled")
	}
	if !strings.Contains(tail.output, "run_command") {
		t.Fatalf("unexpected /audit tail output: %q", tail.output)
	}

	errors := r.executeCommand("/audit errors 1")
	if !errors.handled {
		t.Fatalf("expected /audit errors handled")
	}
	if !strings.Contains(errors.output, "run_command error") {
		t.Fatalf("unexpected /audit errors output: %q", errors.output)
	}
}

func TestDebugToolCallCommand(t *testing.T) {
	r := newMemoryTestRuntime(t)
	r.auditPath = tools.AuditPath(r.cfg.StateDir)
	sink := tools.NewFileAuditSink(r.auditPath)
	sink.RecordToolEvent(context.Background(), tools.ToolAuditEvent{
		Time:         time.Now(),
		Tool:         "read_file",
		Status:       "ok",
		DurationMs:   12,
		ArgsPreview:  "{\"path\":\"README.md\"}",
		OutputSample: "tiny-agent-cli\nREADME snippet",
	})

	result := r.executeCommand("/debug-tool-call")
	if !result.handled {
		t.Fatalf("expected /debug-tool-call handled")
	}
	if !strings.Contains(result.output, "tool=read_file") {
		t.Fatalf("unexpected tool output: %q", result.output)
	}
	if !strings.Contains(result.output, "args={\"path\":\"README.md\"}") {
		t.Fatalf("unexpected args output: %q", result.output)
	}
	if !strings.Contains(result.output, "output=tiny-agent-cli") {
		t.Fatalf("unexpected sample output: %q", result.output)
	}
}

func TestDebugToolCallTailAndErrors(t *testing.T) {
	r := newMemoryTestRuntime(t)
	r.auditPath = tools.AuditPath(r.cfg.StateDir)
	sink := tools.NewFileAuditSink(r.auditPath)
	sink.RecordToolEvent(context.Background(), tools.ToolAuditEvent{
		Time:         time.Now().Add(-2 * time.Second),
		Tool:         "read_file",
		Status:       "ok",
		ArgsPreview:  "{\"path\":\"a.txt\"}",
		OutputSample: "alpha",
	})
	sink.RecordToolEvent(context.Background(), tools.ToolAuditEvent{
		Time:         time.Now().Add(-1 * time.Second),
		Tool:         "run_command",
		Status:       "error",
		ArgsPreview:  "{\"command\":\"false\"}",
		OutputSample: "exit status 1",
		Error:        "command failed",
	})

	tail := r.executeCommand("/debug-tool-call tail 2")
	if !tail.handled {
		t.Fatalf("expected /debug-tool-call tail handled")
	}
	if !strings.Contains(tail.output, "tool=read_file") || !strings.Contains(tail.output, "tool=run_command") {
		t.Fatalf("unexpected tail output: %q", tail.output)
	}

	errors := r.executeCommand("/debug-tool-call errors 1")
	if !errors.handled {
		t.Fatalf("expected /debug-tool-call errors handled")
	}
	if !strings.Contains(errors.output, "tool=run_command") || !strings.Contains(errors.output, "error=command failed") {
		t.Fatalf("unexpected errors output: %q", errors.output)
	}
}

func TestDebugToolCallReplay(t *testing.T) {
	r := newMemoryTestRuntime(t)
	dir := t.TempDir()
	r.cfg.WorkDir = dir
	r.auditPath = tools.AuditPath(r.cfg.StateDir)
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello replay"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	sink := tools.NewFileAuditSink(r.auditPath)
	sink.RecordToolEvent(context.Background(), tools.ToolAuditEvent{
		Time:         time.Now(),
		Tool:         "read_file",
		Status:       "ok",
		InputJSON:    `{"path":"note.txt"}`,
		ArgsPreview:  `{"path":"note.txt"}`,
		OutputSample: "hello replay",
	})

	result := r.executeCommand("/debug-tool-call replay")
	if !result.handled {
		t.Fatalf("expected /debug-tool-call replay handled")
	}
	if !strings.Contains(result.output, "replayed tool=read_file") {
		t.Fatalf("unexpected replay output: %q", result.output)
	}
	if !strings.Contains(result.output, "hello replay") {
		t.Fatalf("expected replayed tool output, got %q", result.output)
	}
}

func TestDebugTurnCommand(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello turn"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	client := &scriptedChatClient{
		responses: []model.Response{
			{
				Choices: []model.Choice{
					{
						Message: model.Message{
							ToolCalls: []model.ToolCall{{
								ID:   "call-1",
								Type: "function",
								Function: model.ToolFunction{
									Name:      "read_file",
									Arguments: `{"path":"note.txt"}`,
								},
							}},
						},
					},
				},
			},
			{
				Choices: []model.Choice{
					{
						Message: model.Message{Content: "done"},
					},
				},
			},
		},
	}
	loop := agent.New(client, tools.NewRegistry(dir, "bash", time.Second, nil), 32768, nil)
	session := loop.NewSession()
	if _, err := session.RunTask(context.Background(), "read note"); err != nil {
		t.Fatalf("run task: %v", err)
	}

	r := newMemoryTestRuntime(t)
	r.loop = loop
	r.session = session

	result := r.executeCommand("/debug-turn tail 2")
	if !result.handled {
		t.Fatalf("expected /debug-turn handled")
	}
	if !strings.Contains(result.output, "decision=execute_tools") {
		t.Fatalf("unexpected debug turn output: %q", result.output)
	}
	if !strings.Contains(result.output, "tools=read_file") {
		t.Fatalf("unexpected debug turn tools: %q", result.output)
	}
	if !strings.Contains(result.output, "decision=finish") {
		t.Fatalf("expected final turn summary, got %q", result.output)
	}
}

func TestSavedSessionStateCanResumeToolWorkflow(t *testing.T) {
	dir := t.TempDir()
	client1 := &scriptedChatClient{
		responses: []model.Response{
			{
				Choices: []model.Choice{
					{
						Message: model.Message{
							ToolCalls: []model.ToolCall{{
								ID:   "call-1",
								Type: "function",
								Function: model.ToolFunction{
									Name:      "write_file",
									Arguments: `{"path":"session-note.txt","content":"resume-me"}`,
								},
							}},
						},
					},
				},
			},
			{
				Choices: []model.Choice{
					{
						Message: model.Message{Content: "note written"},
					},
				},
			},
		},
	}
	r1 := newScriptedRuntime(t, dir, "resume-tools", client1)
	if output, err := r1.executeTask(context.Background(), "Create the session note."); err != nil {
		t.Fatalf("first executeTask: %v", err)
	} else if output != "note written" {
		t.Fatalf("unexpected first output: %q", output)
	}

	state, err := session.Load(r1.statePath)
	if err != nil {
		t.Fatalf("load saved session: %v", err)
	}
	if len(state.Messages) == 0 {
		t.Fatalf("expected saved session messages")
	}

	client2 := &scriptedChatClient{
		responses: []model.Response{
			{
				Choices: []model.Choice{
					{
						Message: model.Message{
							ToolCalls: []model.ToolCall{{
								ID:   "call-2",
								Type: "function",
								Function: model.ToolFunction{
									Name:      "read_file",
									Arguments: `{"path":"session-note.txt"}`,
								},
							}},
						},
					},
				},
			},
			{
				Choices: []model.Choice{
					{
						Message: model.Message{Content: "resume verified"},
					},
				},
			},
		},
	}
	r2 := newScriptedRuntime(t, dir, "resume-tools", client2)
	r2.session.ReplaceMessages(state.Messages)

	if output, err := r2.executeTask(context.Background(), "Continue and verify the session note."); err != nil {
		t.Fatalf("second executeTask: %v", err)
	} else if output != "resume verified" {
		t.Fatalf("unexpected second output: %q", output)
	}
	if len(client2.log) == 0 {
		t.Fatalf("expected restored session to make a model request")
	}

	foundPriorWrite := false
	for _, msg := range client2.log[0].Messages {
		if msg.Role == "tool" && strings.Contains(model.ContentString(msg.Content), "session-note.txt") {
			foundPriorWrite = true
			break
		}
	}
	if !foundPriorWrite {
		t.Fatalf("expected restored request to include prior tool history: %#v", client2.log[0].Messages)
	}
}

func TestResumeCommandRestoresSavedMemory(t *testing.T) {
	dir := t.TempDir()
	r := newScriptedRuntime(t, dir, "source", &scriptedChatClient{})

	if result := r.executeCommand("/memory remember original-note"); !result.handled {
		t.Fatalf("expected memory command to be handled")
	}
	if result := r.executeCommand("/new other"); !result.handled || !strings.Contains(result.output, "started conversation other") {
		t.Fatalf("unexpected switch result: %#v", result)
	}
	if result := r.executeCommand("/memory remember other-note"); !result.handled {
		t.Fatalf("expected memory command to be handled")
	}
	if result := r.executeCommand("/resume source"); !result.handled || !strings.Contains(result.output, "conversation resumed: source") {
		t.Fatalf("unexpected load result: %#v", result)
	}

	show := r.executeCommand("/memory show")
	if !show.handled {
		t.Fatalf("expected memory show handled")
	}
	if !strings.Contains(show.output, "original-note") {
		t.Fatalf("expected restored memory note, got %q", show.output)
	}
	if strings.Contains(show.output, "other-note") {
		t.Fatalf("expected other session memory to be replaced, got %q", show.output)
	}
}

func TestResumeConversationRestoresPlanningState(t *testing.T) {
	dir := t.TempDir()
	r := newScriptedRuntime(t, dir, "source", &scriptedChatClient{})
	if err := r.loop.ReplaceTodo([]tools.TodoItem{{Text: "stale task", Status: "pending"}}); err != nil {
		t.Fatalf("seed stale todo: %v", err)
	}
	if err := r.loop.ReplaceTaskContract(tools.TaskContract{
		Objective: "stale contract",
		Deliverables: []tools.ContractItem{
			{Text: "old deliverable", Status: "pending"},
		},
	}); err != nil {
		t.Fatalf("seed stale contract: %v", err)
	}
	if err := session.Save(session.SessionPath(dir, "source"), session.State{
		SessionName: "source",
		Model:       "test-model",
		TodoItems: []tools.TodoItem{
			{Text: "inspect current issue", Status: "in_progress"},
		},
		TaskContract: tools.TaskContract{
			Objective: "Debug duplicated answers",
			AcceptanceChecks: []tools.ContractItem{
				{Text: "identify root cause", Status: "pending"},
			},
		},
		Messages: []model.Message{
			{Role: "system", Content: "system prompt"},
			{Role: "user", Content: "help"},
		},
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	if loaded, err := r.resumeConversation("source"); err != nil || !loaded {
		t.Fatalf("resume conversation failed: loaded=%t err=%v", loaded, err)
	}

	todos := r.loop.TodoItems()
	if len(todos) != 1 || todos[0].Text != "inspect current issue" {
		t.Fatalf("unexpected restored todo: %#v", todos)
	}
	contract := r.loop.TaskContract()
	if contract.Objective != "Debug duplicated answers" {
		t.Fatalf("unexpected restored contract: %#v", contract)
	}
}

func TestRenameAndForkConversationCommands(t *testing.T) {
	dir := t.TempDir()
	r := newScriptedRuntime(t, dir, "source", &scriptedChatClient{})
	sourceSessionID := r.sessionID
	if sourceSessionID == "" {
		t.Fatalf("expected source session id")
	}
	r.session.ReplaceMessages([]model.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "world"},
	})
	if err := r.save(); err != nil {
		t.Fatalf("save source: %v", err)
	}

	if result := r.executeCommand("/rename renamed"); !result.handled || !strings.Contains(result.output, "renamed conversation source -> renamed") {
		t.Fatalf("unexpected rename result: %#v", result)
	}
	if _, err := os.Stat(session.SessionPath(dir, "renamed")); err != nil {
		t.Fatalf("expected renamed conversation file: %v", err)
	}
	if _, err := os.Stat(session.SessionPath(dir, "source")); !os.IsNotExist(err) {
		t.Fatalf("expected old conversation file removed, got %v", err)
	}

	if result := r.executeCommand("/fork forked"); !result.handled || !strings.Contains(result.output, "forked conversation to forked") {
		t.Fatalf("unexpected fork result: %#v", result)
	}
	if r.sessionName != "forked" {
		t.Fatalf("expected current conversation to switch to forked, got %q", r.sessionName)
	}
	forked, err := session.Load(session.SessionPath(dir, "forked"))
	if err != nil {
		t.Fatalf("load forked conversation: %v", err)
	}
	if !strings.Contains(model.ContentString(forked.Messages[1].Content), "hello") {
		t.Fatalf("expected forked conversation to keep history, got %#v", forked.Messages)
	}
	if strings.TrimSpace(forked.SessionID) == "" {
		t.Fatalf("expected forked session id to be persisted")
	}
	if forked.ParentSession != sourceSessionID {
		t.Fatalf("expected forked parent to be source session id %q, got %q", sourceSessionID, forked.ParentSession)
	}
}

func TestTreeCommandShowsConversationHierarchy(t *testing.T) {
	dir := t.TempDir()
	r := newScriptedRuntime(t, dir, "root", &scriptedChatClient{})
	r.session.ReplaceMessages([]model.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "root"},
	})
	if err := r.save(); err != nil {
		t.Fatalf("save root: %v", err)
	}
	rootID := r.sessionID
	if strings.TrimSpace(rootID) == "" {
		t.Fatalf("expected root session id")
	}

	if result := r.executeCommand("/fork child"); !result.handled {
		t.Fatalf("expected fork command handled")
	}
	tree := r.executeCommand("/tree")
	if !tree.handled {
		t.Fatalf("expected tree command handled")
	}
	if !strings.Contains(tree.output, "conversation tree:") {
		t.Fatalf("expected tree header, got %q", tree.output)
	}
	if !strings.Contains(tree.output, "root ["+rootID+"]") {
		t.Fatalf("expected root node with id in tree output: %q", tree.output)
	}
	if !strings.Contains(tree.output, "child") {
		t.Fatalf("expected child node in tree output: %q", tree.output)
	}
	if !strings.Contains(tree.output, "(current)") {
		t.Fatalf("expected current marker in tree output: %q", tree.output)
	}
}

func TestResetClearsPlanningState(t *testing.T) {
	r := newMemoryTestRuntime(t)
	if err := r.loop.ReplaceTodo([]tools.TodoItem{{Text: "investigate fish page", Status: "in_progress"}}); err != nil {
		t.Fatalf("replace todo: %v", err)
	}
	if err := r.loop.ReplaceTaskContract(tools.TaskContract{
		Objective: "Fix fish page",
		Deliverables: []tools.ContractItem{
			{Text: "remove stale fish task", Status: "pending"},
		},
	}); err != nil {
		t.Fatalf("replace contract: %v", err)
	}

	result := r.executeCommand("/reset")
	if !result.handled {
		t.Fatalf("expected /reset handled")
	}
	if got := r.loop.TodoItems(); len(got) != 0 {
		t.Fatalf("expected reset to clear todo items, got %#v", got)
	}
	if got := r.loop.TaskContract(); !isEmptyTaskContract(got) {
		t.Fatalf("expected reset to clear task contract, got %#v", got)
	}
}

func TestBgRoleUsage(t *testing.T) {
	r := newMemoryTestRuntime(t)
	r.jobs = newJobManager(config.Config{ApprovalMode: tools.ApprovalDangerously}, "")
	result := r.executeCommand("/bg-role")
	if !result.handled {
		t.Fatalf("expected /bg-role handled")
	}
	if !strings.Contains(result.output, "/bg-role") {
		t.Fatalf("unexpected usage output: %q", result.output)
	}
}

func TestMCPCommandAddAndRemove(t *testing.T) {
	dir := t.TempDir()
	r := newMemoryTestRuntime(t)
	r.cfg.WorkDir = dir

	added := r.executeCommand("/mcp add demo demo-server --flag")
	if !added.handled {
		t.Fatalf("expected /mcp add handled")
	}
	if !strings.Contains(added.output, "saved MCP server demo") {
		t.Fatalf("unexpected add output: %q", added.output)
	}

	state, err := mcp.Load(mcp.Path(filepath.Join(dir, ".tacli")))
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if len(state.Servers) != 1 || state.Servers[0].Name != "demo" {
		t.Fatalf("unexpected state: %#v", state.Servers)
	}

	removed := r.executeCommand("/mcp remove demo")
	if !removed.handled {
		t.Fatalf("expected /mcp remove handled")
	}
	if !strings.Contains(removed.output, "removed MCP server demo") {
		t.Fatalf("unexpected remove output: %q", removed.output)
	}
}

func TestAgentsCommandListsSnapshots(t *testing.T) {
	r := newMemoryTestRuntime(t)
	r.jobs = newJobManager(config.Config{ApprovalMode: tools.ApprovalDangerously}, "")
	r.jobs.orchestration.Register(agent.SubagentSnapshot{
		ID:        "job-001",
		Status:    "ready",
		Role:      "explore",
		Model:     "test-model",
		TaskCount: 2,
		Session: agent.SubagentSessionState{
			MessageCount: 5,
		},
	}, nil)

	result := r.executeCommand("/agents")
	if !result.handled {
		t.Fatalf("expected /agents handled")
	}
	if !strings.Contains(result.output, "job-001  ready") {
		t.Fatalf("unexpected output: %q", result.output)
	}
	if !strings.Contains(result.output, "role=explore") {
		t.Fatalf("missing role: %q", result.output)
	}
}

func TestReloadPluginsCommandWithoutLoadedPlugins(t *testing.T) {
	r := newMemoryTestRuntime(t)
	manager, err := plugins.NewManager()
	if err != nil {
		t.Fatalf("new plugin manager: %v", err)
	}
	r.pluginManager = manager
	if got := r.reloadPluginsCommand(); got != "reloaded 0 plugins" {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestParseNaturalLanguageMemoryIntentRemember(t *testing.T) {
	intent, ok := parseNaturalLanguageMemoryIntent("记住以后默认中文简洁回答")
	if !ok {
		t.Fatalf("expected remember intent")
	}
	if intent.action != memoryActionRemember {
		t.Fatalf("unexpected action: %q", intent.action)
	}
	if intent.scope != memoryScopeGlobal {
		t.Fatalf("unexpected scope: %q", intent.scope)
	}
	if intent.body != "以后默认中文简洁回答" {
		t.Fatalf("unexpected body: %q", intent.body)
	}
}

func TestParseNaturalLanguageMemoryIntentShow(t *testing.T) {
	intent, ok := parseNaturalLanguageMemoryIntent("查看记忆")
	if !ok {
		t.Fatalf("expected show intent")
	}
	if intent.action != memoryActionShow {
		t.Fatalf("unexpected action: %q", intent.action)
	}
}

func TestExecuteTaskNaturalLanguageRemember(t *testing.T) {
	r := newMemoryTestRuntime(t)
	output, err := r.executeTask(context.Background(), "记住以后默认中文简洁回答")
	if err != nil {
		t.Fatalf("executeTask failed: %v", err)
	}
	if output != i18n.T("mem.saved.global") {
		t.Fatalf("unexpected output: %q", output)
	}
	if !reflect.DeepEqual(r.globalMemory, []string{"以后默认中文简洁回答"}) {
		t.Fatalf("unexpected global memory: %#v", r.globalMemory)
	}
}

func TestExecuteTaskNaturalLanguageForgetLast(t *testing.T) {
	r := newMemoryTestRuntime(t)
	r.projectMemory = []string{"当前项目使用 Go", "当前项目需要离线优先"}
	r.refreshMemoryContext()
	output, err := r.executeTask(context.Background(), "忘掉刚才那条")
	if err != nil {
		t.Fatalf("executeTask failed: %v", err)
	}
	if output != i18n.T("mem.deleted.last.project") {
		t.Fatalf("unexpected output: %q", output)
	}
	if !reflect.DeepEqual(r.projectMemory, []string{"当前项目使用 Go"}) {
		t.Fatalf("unexpected project memory: %#v", r.projectMemory)
	}
}

func TestExecuteTaskNaturalLanguageShowMemory(t *testing.T) {
	r := newMemoryTestRuntime(t)
	r.globalMemory = []string{"Prefer concise Chinese answers"}
	r.projectMemory = []string{"This repo uses Go"}
	r.refreshMemoryContext()
	output, err := r.executeTask(context.Background(), "show memory")
	if err != nil {
		t.Fatalf("executeTask failed: %v", err)
	}
	if !strings.Contains(output, "Global memory:") || !strings.Contains(output, "Project memory:") {
		t.Fatalf("unexpected output: %q", output)
	}
}

func newMemoryTestRuntime(t *testing.T) *chatRuntime {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{
		Model:          "test-model",
		BaseURL:        "http://127.0.0.1:11434/v1",
		WorkDir:        dir,
		StateDir:       dir,
		ContextWindow:  32768,
		MaxSteps:       4,
		CommandTimeout: time.Second,
		Shell:          "bash",
		ApprovalMode:   tools.ApprovalConfirm,
	}
	loop := agent.New(chatClientStub{}, tools.NewRegistry(dir, "bash", time.Second, nil, nil), 32768, nil)
	r := &chatRuntime{
		cfg:            cfg,
		reader:         bufio.NewReader(strings.NewReader("")),
		approver:       tools.NewTerminalApprover(bufio.NewReader(strings.NewReader("")), os.Stderr, tools.ApprovalConfirm, true),
		loop:           loop,
		session:        loop.NewSession(),
		sessionName:    "chat-test",
		sessionID:      session.NewSessionID(),
		outputMode:     "terminal",
		transcriptPath: session.TranscriptPath(dir, "chat-test"),
		statePath:      session.SessionPath(dir, "chat-test"),
		memoryPath:     memory.Path(dir),
		scopeKey:       memory.ScopeKey(dir),
	}
	r.refreshMemoryContext()
	return r
}

func TestShouldAutoStartExplore(t *testing.T) {
	cfg := config.Config{ApprovalMode: tools.ApprovalDangerously}
	jobs := newJobManager(cfg, "")
	if !shouldAutoStartExplore("Analyze this repository and identify the highest-risk code paths and architecture issues.", cfg, jobs) {
		t.Fatalf("expected auto explore to trigger")
	}
	if shouldAutoStartExplore("Read one file and fix one line.", cfg, jobs) {
		t.Fatalf("expected small task not to trigger")
	}
	if shouldAutoStartExplore("Analyze this repository deeply.", config.Config{ApprovalMode: tools.ApprovalConfirm}, jobs) {
		t.Fatalf("expected confirm mode not to trigger")
	}
}

func TestBuildAutoExploreTask(t *testing.T) {
	got := buildAutoExploreTask("Analyze the auth flow")
	if !strings.Contains(got, "read-only mode") {
		t.Fatalf("missing read-only instruction: %q", got)
	}
	if !strings.Contains(got, "Request: Analyze the auth flow") {
		t.Fatalf("missing request echo: %q", got)
	}
}

func TestInjectOrchestratorNote(t *testing.T) {
	r := newMemoryTestRuntime(t)
	r.injectOrchestratorNote("job-001")
	msgs := r.session.Messages()
	last := msgs[len(msgs)-1]
	if last.Role != "user" {
		t.Fatalf("unexpected role: %q", last.Role)
	}
	if !strings.HasPrefix(model.ContentString(last.Content), "<system-reminder>internal-orchestration") {
		t.Fatalf("missing system reminder prefix: %q", model.ContentString(last.Content))
	}
	if !strings.Contains(model.ContentString(last.Content), "job-001") {
		t.Fatalf("missing job id in note: %q", model.ContentString(last.Content))
	}
}

func TestMaybeApplyReadyJobSummaries(t *testing.T) {
	r := newMemoryTestRuntime(t)
	r.jobs = newJobManager(config.Config{ApprovalMode: tools.ApprovalDangerously}, "")
	r.jobs.jobs["job-001"] = &backgroundJob{
		id:         "job-001",
		status:     jobReady,
		lastOutput: "Key findings:\n- found one issue",
		summary:    jobSummary{Findings: []string{"found one issue"}},
		createdAt:  time.Now(),
		updatedAt:  time.Now(),
	}

	r.maybeApplyReadyJobSummaries()
	msgs := r.session.Messages()
	last := msgs[len(msgs)-1]
	if last.Role != "user" {
		t.Fatalf("unexpected role: %q", last.Role)
	}
	if !strings.HasPrefix(model.ContentString(last.Content), "<system-reminder>background-result") {
		t.Fatalf("missing system reminder prefix: %q", model.ContentString(last.Content))
	}
	if !strings.Contains(model.ContentString(last.Content), "job-001") {
		t.Fatalf("missing job summary: %q", model.ContentString(last.Content))
	}

	r.maybeApplyReadyJobSummaries()
	msgs2 := r.session.Messages()
	if len(msgs2) != len(msgs) {
		t.Fatalf("expected summary to apply once, got %d then %d", len(msgs), len(msgs2))
	}
}

func TestAdvanceTodoWithJobSummaryProgressesPlan(t *testing.T) {
	r := newMemoryTestRuntime(t)
	if err := r.loop.ReplaceTodo([]tools.TodoItem{
		{Text: "inspect auth flow", Status: "in_progress"},
		{Text: "patch token refresh", Status: "pending"},
	}); err != nil {
		t.Fatalf("replace todo: %v", err)
	}

	r.advanceTodoWithJobSummary(jobSnapshot{
		Summary: jobSummary{
			NextSteps: []string{"add regression tests", "patch token refresh"},
		},
	})

	got := r.loop.TodoItems()
	want := []tools.TodoItem{
		{Text: "inspect auth flow", Status: "completed"},
		{Text: "patch token refresh", Status: "pending"},
		{Text: "add regression tests", Status: "pending"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected todo: %#v", got)
	}
}

func TestAdvanceTodoWithJobSummarySeedsPlanWhenEmpty(t *testing.T) {
	r := newMemoryTestRuntime(t)
	r.advanceTodoWithJobSummary(jobSnapshot{
		Summary: jobSummary{
			NextSteps: []string{"inspect config loader", "run focused tests"},
		},
	})
	got := r.loop.TodoItems()
	want := []tools.TodoItem{
		{Text: "inspect config loader", Status: "pending"},
		{Text: "run focused tests", Status: "pending"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected todo seed: %#v", got)
	}
}

func TestBuildAutoExploreTaskRequiresStructuredSections(t *testing.T) {
	got := buildAutoExploreTask("Analyze the auth flow")
	for _, section := range []string{"Key findings:", "Relevant files:", "Risks or unknowns:", "Recommended next steps:"} {
		if !strings.Contains(got, section) {
			t.Fatalf("missing section %q in %q", section, got)
		}
	}
}

func TestBeforeExitCtrlCStyleSkipsAutoMemorySummarization(t *testing.T) {
	r := newMemoryTestRuntime(t)
	r.autoMemoryExit = true
	r.dirtySession = true
	r.projectMemory = []string{"existing note"}

	r.beforeExit(false)

	if r.dirtySession != true {
		t.Fatalf("expected dirty session to remain true when auto memory is skipped")
	}
	if !reflect.DeepEqual(r.projectMemory, []string{"existing note"}) {
		t.Fatalf("unexpected project memory: %#v", r.projectMemory)
	}
}

func TestBeforeExitAutoMemoryFallsBackWithoutProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"filtered","code":"content_filter"}}`)
	}))
	defer server.Close()

	r := newMemoryTestRuntime(t)
	r.cfg.BaseURL = server.URL
	r.autoMemoryExit = true
	r.dirtySession = true
	r.session.ReplaceMessages(append(r.session.Messages(), model.Message{
		Role:    "user",
		Content: "Always answer in Chinese and keep responses concise.",
	}))

	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	defer stderrR.Close()
	oldStderr := os.Stderr
	os.Stderr = stderrW
	r.beforeExit(true)
	_ = stderrW.Close()
	os.Stderr = oldStderr

	stderrBytes, err := io.ReadAll(stderrR)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if strings.Contains(string(stderrBytes), "auto-memory error") {
		t.Fatalf("expected no auto-memory error, got %q", string(stderrBytes))
	}
	if len(r.projectMemory) == 0 {
		t.Fatalf("expected fallback memory note to be stored")
	}
	if r.dirtySession {
		t.Fatalf("expected dirty session to be cleared")
	}
}

func TestBeforeExitAutoMemorySilentlySkipsProviderErrorWithoutFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"filtered","code":"content_filter"}}`)
	}))
	defer server.Close()

	r := newMemoryTestRuntime(t)
	r.cfg.BaseURL = server.URL
	r.autoMemoryExit = true
	r.dirtySession = true
	r.session.ReplaceMessages(append(r.session.Messages(), model.Message{
		Role:    "user",
		Content: "What time is it now?",
	}))

	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	defer stderrR.Close()
	oldStderr := os.Stderr
	os.Stderr = stderrW
	r.beforeExit(true)
	_ = stderrW.Close()
	os.Stderr = oldStderr

	stderrBytes, err := io.ReadAll(stderrR)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if strings.Contains(string(stderrBytes), "auto-memory error") {
		t.Fatalf("expected no auto-memory error, got %q", string(stderrBytes))
	}
	if len(r.projectMemory) != 0 {
		t.Fatalf("expected no fallback notes, got %#v", r.projectMemory)
	}
	if r.dirtySession {
		t.Fatalf("expected dirty session to be cleared")
	}
}

func TestInterruptCommandCancelsForegroundTask(t *testing.T) {
	r := newMemoryTestRuntime(t)
	canceled := false
	r.setForegroundCancel(func() {
		canceled = true
	})

	result := r.executeCommand("/interrupt")
	if !result.handled {
		t.Fatalf("expected /interrupt handled")
	}
	if result.output != i18n.T("cmd.interrupt.ok") {
		t.Fatalf("unexpected output: %q", result.output)
	}
	if !canceled {
		t.Fatalf("expected foreground cancel to run")
	}
}

func TestInterruptCommandReportsIdleWhenNoForegroundTask(t *testing.T) {
	r := newMemoryTestRuntime(t)

	result := r.executeCommand("/interrupt")
	if !result.handled {
		t.Fatalf("expected /interrupt handled")
	}
	if result.output != i18n.T("cmd.interrupt.idle") {
		t.Fatalf("unexpected output: %q", result.output)
	}
}

func TestRenderSessionRecordIncludesMessagesAndToolCalls(t *testing.T) {
	r := newMemoryTestRuntime(t)
	r.session.ReplaceMessages([]model.Message{
		{Role: "system", Content: "system prompt contents"},
		{Role: "user", Content: "build a repro"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: model.ToolFunction{
				Name:      "read_file",
				Arguments: `{"path":"README.md"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-1", Content: "tiny-agent-cli\nREADME snippet"},
		{Role: "assistant", Content: "done"},
	})

	got := r.renderSessionRecord()
	if !strings.Contains(got, "# tacli conversation record") {
		t.Fatalf("missing header: %q", got)
	}
	if !strings.Contains(got, "conversation=chat-test") {
		t.Fatalf("missing conversation line: %q", got)
	}
	if !strings.Contains(got, "(initial system prompt omitted") {
		t.Fatalf("expected initial system prompt omission, got %q", got)
	}
	if !strings.Contains(got, "### 2 user") || !strings.Contains(got, "build a repro") {
		t.Fatalf("missing user message: %q", got)
	}
	if !strings.Contains(got, `tool_call read_file {"path":"README.md"}`) {
		t.Fatalf("missing tool call: %q", got)
	}
	if !strings.Contains(got, "tool_call_id=call-1") || !strings.Contains(got, "README snippet") {
		t.Fatalf("missing tool result: %q", got)
	}
}

func TestSaveCommandWritesExportFile(t *testing.T) {
	r := newMemoryTestRuntime(t)
	r.session.ReplaceMessages([]model.Message{
		{Role: "system", Content: "system prompt contents"},
		{Role: "user", Content: "please inspect this failure"},
		{Role: "assistant", Content: "I found one issue."},
	})

	result := r.executeCommand("/save")
	if !result.handled {
		t.Fatalf("expected /save handled")
	}
	if !strings.Contains(result.output, "conversation record saved") {
		t.Fatalf("expected save output, got %q", result.output)
	}
	wantPath := "/tmp/session-data.txt"
	if !strings.Contains(result.output, wantPath) {
		t.Fatalf("expected export path in output, got %q", result.output)
	}
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "please inspect this failure") || !strings.Contains(text, "I found one issue.") {
		t.Fatalf("unexpected export text: %q", text)
	}
}

func TestRunChatProcessesNulSeparatedCommandsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_SETTINGS_SYNC", "false")

	stdinFile, err := os.CreateTemp(t.TempDir(), "chat-input-*.txt")
	if err != nil {
		t.Fatalf("create stdin file: %v", err)
	}
	input := "/memory remember alpha\x00/memory remember beta\x00/memory show\n"
	if _, err := stdinFile.WriteString(input); err != nil {
		t.Fatalf("write stdin file: %v", err)
	}
	if _, err := stdinFile.Seek(0, 0); err != nil {
		t.Fatalf("rewind stdin file: %v", err)
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

	oldStdin, oldStdout, oldStderr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = stdinFile, stdoutW, stderrW
	defer func() {
		os.Stdin, os.Stdout, os.Stderr = oldStdin, oldStdout, oldStderr
	}()

	code := runChat([]string{
		"--workdir", dir,
		"--base-url", "http://example.test/v1",
		"--model", "test-model",
		"--api-key", "test-key",
		"--resume", "nul-batch",
	})
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
		t.Fatalf("runChat exit code = %d, stderr=%q", code, string(stderrBytes))
	}
	stdoutText := string(stdoutBytes)
	if !strings.Contains(stdoutText, "project_notes=2") {
		t.Fatalf("expected both NUL-separated commands to run, got %q", stdoutText)
	}
	if !strings.Contains(stdoutText, "alpha") || !strings.Contains(stdoutText, "beta") {
		t.Fatalf("expected remembered notes in stdout output, got %q", stdoutText)
	}
}

func newScriptedRuntime(t *testing.T, dir, sessionName string, client *scriptedChatClient) *chatRuntime {
	t.Helper()
	cfg := config.Config{
		Model:          "test-model",
		BaseURL:        "http://127.0.0.1:11434/v1",
		APIKey:         "test-key",
		WorkDir:        dir,
		StateDir:       dir,
		ContextWindow:  32768,
		MaxSteps:       4,
		CommandTimeout: time.Second,
		ModelTimeout:   time.Second,
		Shell:          "bash",
		ApprovalMode:   tools.ApprovalDangerously,
		SettingsSync:   false,
	}
	loop := agent.New(client, tools.NewRegistry(dir, "bash", time.Second, nil, nil), 32768, nil)
	r := &chatRuntime{
		cfg:            cfg,
		reader:         bufio.NewReader(strings.NewReader("")),
		approver:       tools.NewTerminalApprover(bufio.NewReader(strings.NewReader("")), os.Stderr, tools.ApprovalDangerously, true),
		loop:           loop,
		session:        loop.NewSession(),
		sessionName:    sessionName,
		sessionID:      session.NewSessionID(),
		outputMode:     "raw",
		transcriptPath: session.TranscriptPath(dir, sessionName),
		statePath:      session.SessionPath(dir, sessionName),
		memoryPath:     memory.Path(dir),
		scopeKey:       memory.ScopeKey(dir),
	}
	r.refreshMemoryContext()
	return r
}
