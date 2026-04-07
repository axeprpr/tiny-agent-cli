package main

import (
	"bufio"
	"context"
	"fmt"
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

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "blank creates new session", input: "", want: "chat-20260320-060506"},
		{name: "new creates new session", input: "new", want: "chat-20260320-060506"},
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

	output := r.policyCommand([]string{"/policy", "default", "allow"})
	if !strings.Contains(output, "default=allow") {
		t.Fatalf("unexpected default policy output: %q", output)
	}

	output = r.policyCommand([]string{"/policy", "tool", "write_file", "deny"})
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
	if last.Role != "system" {
		t.Fatalf("unexpected role: %q", last.Role)
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
	if last.Role != "system" {
		t.Fatalf("unexpected role: %q", last.Role)
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
