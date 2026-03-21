package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"tiny-agent-cli/internal/agent"
	"tiny-agent-cli/internal/config"
	"tiny-agent-cli/internal/memory"
	"tiny-agent-cli/internal/model"
	"tiny-agent-cli/internal/session"
	"tiny-agent-cli/internal/tools"
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
		{name: "blank creates new session", input: "", want: "chat-20260320-140506"},
		{name: "new creates new session", input: "new", want: "chat-20260320-140506"},
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

func TestSaveDoesNotPersistApprovalMode(t *testing.T) {
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
		cfg:         cfg,
		approver:    tools.NewTerminalApprover(bufio.NewReader(strings.NewReader("")), os.Stderr, tools.ApprovalDangerously, true),
		session:     agentSessionStub(),
		sessionName: "chat-test",
		statePath:   session.SessionPath(dir, "chat-test"),
		outputMode:  "terminal",
	}
	if err := r.save(); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	state, err := session.Load(r.statePath)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if state.ApprovalMode != "" {
		t.Fatalf("approval mode should not persist, got %q", state.ApprovalMode)
	}
	if state.Model != "" {
		t.Fatalf("model should not persist, got %q", state.Model)
	}
	if state.OutputMode != "" {
		t.Fatalf("output mode should not persist, got %q", state.OutputMode)
	}
}

func agentSessionStub() *agent.Session {
	a := agent.New(chatClientStub{}, tools.NewRegistry(".", "bash", time.Second, nil), 1, nil)
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
	if output != "已记住为全局偏好。" {
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
	if output != "已删除最近一条项目记忆。" {
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
	loop := agent.New(chatClientStub{}, tools.NewRegistry(dir, "bash", time.Second, nil, nil), 1, nil)
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
