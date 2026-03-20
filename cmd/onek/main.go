package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"onek-agent/internal/agent"
	"onek-agent/internal/config"
	"onek-agent/internal/memory"
	"onek-agent/internal/model"
	"onek-agent/internal/model/openaiapi"
	"onek-agent/internal/session"
	"onek-agent/internal/tools"
)

var version = "dev"

type runtimeOptions struct {
	outputMode     string
	session        string
	autoMemoryExit bool
}

type chatRuntime struct {
	cfg            config.Config
	reader         *bufio.Reader
	approver       tools.Approver
	loop           *agent.Agent
	session        *agent.Session
	sessionName    string
	outputMode     string
	transcriptPath string
	statePath      string
	memoryPath     string
	scopeKey       string
	globalMemory   []string
	projectMemory  []string
	autoMemoryExit bool
	dirtySession   bool
}

const (
	memorySummaryMaxMessages = 24
	memorySummaryMaxChars    = 8000
	memorySummaryEntryChars  = 320
	autoMemoryTimeout        = 20 * time.Second
)

var memoryCandidateSplit = regexp.MustCompile(`[\n\r.!?。！？;；]+`)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	args, globalDangerously := peelGlobalDangerously(args)
	if len(args) == 0 {
		if tools.IsInteractiveTerminal(os.Stdin) {
			return runChat(withDangerouslyFlag(nil, globalDangerously))
		}
		printUsage()
		return 2
	}

	switch args[0] {
	case "run":
		return runTask(withDangerouslyFlag(args[1:], globalDangerously))
	case "chat":
		return runChat(withDangerouslyFlag(args[1:], globalDangerously))
	case "ping":
		return pingModel(args[1:])
	case "models":
		return listModels(args[1:])
	case "version", "--version", "-version":
		fmt.Println(version)
		return 0
	case "help", "-h", "--help":
		printUsage()
		return 0
	default:
		return runTask(withDangerouslyFlag(args, globalDangerously))
	}
}

func peelGlobalDangerously(args []string) ([]string, bool) {
	if len(args) == 0 {
		return args, false
	}
	rest := make([]string, 0, len(args))
	dangerously := false
	for _, arg := range args {
		switch arg {
		case "-dangerously", "--dangerously", "-d":
			dangerously = true
		default:
			rest = append(rest, arg)
		}
	}
	return rest, dangerously
}

func withDangerouslyFlag(args []string, enable bool) []string {
	if !enable {
		return args
	}
	out := make([]string, 0, len(args)+1)
	out = append(out, "--dangerously")
	out = append(out, args...)
	return out
}

func runTask(args []string) int {
	cfg, opts, taskArgs, reader, err := parseAgentFlags("run", args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid config: %v\n", err)
		return 2
	}

	task := strings.TrimSpace(strings.Join(taskArgs, " "))
	if task == "" {
		fmt.Fprintln(os.Stderr, "missing task")
		printRunUsage()
		return 2
	}

	loop, _ := buildAgent(cfg, reader)
	result, err := loop.Run(context.Background(), task)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
		return 1
	}

	fmt.Println(formatRunOutput(result.Final, opts.outputMode))
	return 0
}

func runChat(args []string) int {
	cfg, opts, _, reader, err := parseAgentFlags("chat", args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid config: %v\n", err)
		return 2
	}

	runtime, err := newChatRuntime(cfg, opts, reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "chat setup error: %v\n", err)
		return 1
	}

	interactive := tools.IsInteractiveTerminal(os.Stdin)
	if interactive {
		return runChatTUI(runtime)
	}

	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && len(line) == 0 {
			return 0
		}

		task := strings.TrimSpace(line)
		if task == "" {
			if readErr != nil {
				return 0
			}
			continue
		}

		if strings.HasPrefix(task, "/") {
			result := runtime.executeCommand(task)
			if result.handled {
				if strings.TrimSpace(result.output) != "" {
					fmt.Fprintln(os.Stderr, result.output)
				}
				if result.exitCode >= 0 {
					runtime.beforeExit()
					return result.exitCode
				}
				if readErr != nil {
					runtime.beforeExit()
					return 0
				}
				continue
			}
		}

		output, err := runtime.executeTask(context.Background(), task)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
		} else {
			fmt.Println(output)
		}

		if readErr != nil {
			runtime.beforeExit()
			return 0
		}
	}
}

func newChatRuntime(cfg config.Config, opts runtimeOptions, reader *bufio.Reader) (*chatRuntime, error) {
	loop, approver := buildAgent(cfg, reader)
	sessionName := resolveChatSessionName(opts.session, time.Now())

	r := &chatRuntime{
		cfg:            cfg,
		reader:         reader,
		approver:       approver,
		loop:           loop,
		outputMode:     opts.outputMode,
		autoMemoryExit: opts.autoMemoryExit,
		memoryPath:     memory.Path(cfg.StateDir),
		scopeKey:       memory.ScopeKey(cfg.WorkDir),
	}
	r.setSessionName(sessionName)
	if mem, err := memory.Load(r.memoryPath); err == nil {
		r.globalMemory = mem.Global
		r.projectMemory = mem.Projects[r.scopeKey]
	}
	r.session = r.newSession()

	if strings.TrimSpace(opts.session) != "" {
		if _, err := r.loadSessionState(); err != nil {
			return nil, err
		}
	}

	return r, nil
}

type runtimeCommandResult struct {
	handled  bool
	output   string
	exitCode int
}

func (r *chatRuntime) executeCommand(input string) runtimeCommandResult {
	fields := strings.Fields(strings.TrimSpace(input))
	if len(fields) == 0 {
		return runtimeCommandResult{handled: true, exitCode: -1}
	}

	switch fields[0] {
	case "/exit", "/quit":
		return runtimeCommandResult{handled: true, exitCode: 0}
	case "/reset":
		r.session = r.newSession()
		_ = r.save()
		return runtimeCommandResult{handled: true, output: "context reset", exitCode: -1}
	case "/help":
		return runtimeCommandResult{handled: true, output: strings.Join([]string{
			"/help                     show commands",
			"/exit                     quit",
			"/reset                    clear conversation context",
			"/session [name|new]       switch or create a chat session",
			"/status                   show session settings",
			"/approval confirm|dangerously",
			"/output raw|terminal",
			"/model <name>",
			"/scope                    show current memory scope",
			"/memory                   show saved memory",
			"/remember <text>           add a project memory note",
			"/remember-global <text>    add a global memory note",
			"/forget <query>            remove matching project memory notes",
			"/forget-global <query>     remove matching global memory notes",
			"/memorize                  summarize this session into project memory",
		}, "\n"), exitCode: -1}
	case "/status":
		return runtimeCommandResult{handled: true, output: fmt.Sprintf("session=%s\nmodel=%s\napproval=%s\noutput=%s\nmemory_scope=%s\nglobal_memory_notes=%d\nproject_memory_notes=%d\nstate=%s\ntranscript=%s\nmemory=%s",
			r.sessionName, r.cfg.Model, r.approver.Mode(), r.outputMode, r.scopeKey, len(r.globalMemory), len(r.projectMemory), r.statePath, r.transcriptPath, r.memoryPath), exitCode: -1}
	case "/session":
		if len(fields) == 1 {
			return runtimeCommandResult{handled: true, output: r.describeSessions(), exitCode: -1}
		}
		switchedTo, err := r.switchSession(strings.Join(fields[1:], " "))
		if err != nil {
			return runtimeCommandResult{handled: true, output: fmt.Sprintf("session switch error: %v", err), exitCode: -1}
		}
		return runtimeCommandResult{handled: true, output: switchedTo, exitCode: -1}
	case "/scope":
		return runtimeCommandResult{handled: true, output: r.scopeKey, exitCode: -1}
	case "/approval":
		if len(fields) != 2 {
			return runtimeCommandResult{handled: true, output: "usage: /approval confirm|dangerously", exitCode: -1}
		}
		if err := r.approver.SetMode(fields[1]); err != nil {
			return runtimeCommandResult{handled: true, output: err.Error(), exitCode: -1}
		}
		r.cfg.ApprovalMode = r.approver.Mode()
		_ = r.save()
		return runtimeCommandResult{handled: true, output: fmt.Sprintf("approval mode set to %s", r.approver.Mode()), exitCode: -1}
	case "/output":
		if len(fields) != 2 || (fields[1] != "raw" && fields[1] != "terminal") {
			return runtimeCommandResult{handled: true, output: "usage: /output raw|terminal", exitCode: -1}
		}
		r.outputMode = fields[1]
		_ = r.save()
		return runtimeCommandResult{handled: true, output: fmt.Sprintf("output mode set to %s", r.outputMode), exitCode: -1}
	case "/model":
		if len(fields) < 2 {
			return runtimeCommandResult{handled: true, output: "usage: /model <name>", exitCode: -1}
		}
		r.cfg.Model = strings.Join(fields[1:], " ")
		r.rebuildLoop()
		_ = r.save()
		return runtimeCommandResult{handled: true, output: fmt.Sprintf("model set to %s", r.cfg.Model), exitCode: -1}
	case "/memory":
		return runtimeCommandResult{handled: true, output: memory.FormatNotes(r.globalMemory, r.projectMemory), exitCode: -1}
	case "/remember":
		if len(fields) < 2 {
			return runtimeCommandResult{handled: true, output: "usage: /remember <text>", exitCode: -1}
		}
		r.projectMemory = memory.Add(r.projectMemory, strings.TrimSpace(input[len("/remember"):]))
		r.refreshMemoryContext()
		_ = r.saveMemory()
		_ = r.save()
		return runtimeCommandResult{handled: true, output: "project memory saved", exitCode: -1}
	case "/remember-global":
		if len(fields) < 2 {
			return runtimeCommandResult{handled: true, output: "usage: /remember-global <text>", exitCode: -1}
		}
		r.globalMemory = memory.Add(r.globalMemory, strings.TrimSpace(input[len("/remember-global"):]))
		r.refreshMemoryContext()
		_ = r.saveMemory()
		_ = r.save()
		return runtimeCommandResult{handled: true, output: "global memory saved", exitCode: -1}
	case "/forget":
		if len(fields) < 2 {
			return runtimeCommandResult{handled: true, output: "usage: /forget <query>", exitCode: -1}
		}
		updated, removed := memory.ForgetMatching(r.projectMemory, strings.TrimSpace(input[len("/forget"):]))
		r.projectMemory = updated
		r.refreshMemoryContext()
		_ = r.saveMemory()
		_ = r.save()
		return runtimeCommandResult{handled: true, output: fmt.Sprintf("removed %d project memory note(s)", removed), exitCode: -1}
	case "/forget-global":
		if len(fields) < 2 {
			return runtimeCommandResult{handled: true, output: "usage: /forget-global <query>", exitCode: -1}
		}
		updated, removed := memory.ForgetMatching(r.globalMemory, strings.TrimSpace(input[len("/forget-global"):]))
		r.globalMemory = updated
		r.refreshMemoryContext()
		_ = r.saveMemory()
		_ = r.save()
		return runtimeCommandResult{handled: true, output: fmt.Sprintf("removed %d global memory note(s)", removed), exitCode: -1}
	case "/memorize":
		added, err := r.summarizeMemory()
		if err != nil {
			return runtimeCommandResult{handled: true, output: fmt.Sprintf("memorize error: %v", err), exitCode: -1}
		}
		return runtimeCommandResult{handled: true, output: fmt.Sprintf("added %d memory note(s)", added), exitCode: -1}
	default:
		return runtimeCommandResult{handled: false, exitCode: -1}
	}
}

func (r *chatRuntime) rebuildLoop() {
	r.loop = buildAgentWith(r.cfg, r.approver, os.Stderr)
	r.session.SetAgent(r.loop)
	_ = r.approver.SetMode(r.cfg.ApprovalMode)
}

func (r *chatRuntime) setSessionName(name string) {
	r.sessionName = strings.TrimSpace(name)
	r.transcriptPath = session.TranscriptPath(r.cfg.StateDir, r.sessionName)
	r.statePath = session.SessionPath(r.cfg.StateDir, r.sessionName)
}

func (r *chatRuntime) loadSessionState() (bool, error) {
	state, err := session.Load(r.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	if strings.TrimSpace(state.Model) != "" && state.Model != r.cfg.Model {
		r.cfg.Model = state.Model
		r.rebuildLoop()
	}
	if len(state.Messages) > 0 {
		r.session.ReplaceMessages(state.Messages)
	}
	if strings.TrimSpace(state.OutputMode) != "" {
		r.outputMode = state.OutputMode
	}
	if strings.TrimSpace(state.ApprovalMode) != "" {
		r.cfg.ApprovalMode = state.ApprovalMode
		_ = r.approver.SetMode(state.ApprovalMode)
	}
	return true, nil
}

func (r *chatRuntime) switchSession(name string) (string, error) {
	next := resolveChatSessionName(name, time.Now())
	if next == r.sessionName {
		return fmt.Sprintf("already on session %s", r.sessionName), nil
	}
	if err := r.save(); err != nil {
		return "", err
	}

	r.setSessionName(next)
	r.session = r.newSession()
	r.dirtySession = false

	loaded, err := r.loadSessionState()
	if err != nil {
		return "", err
	}
	if err := r.save(); err != nil {
		return "", err
	}
	if loaded {
		return fmt.Sprintf("switched to session %s", r.sessionName), nil
	}
	return fmt.Sprintf("started session %s", r.sessionName), nil
}

func (r *chatRuntime) describeSessions() string {
	lines := []string{
		"current=" + r.sessionName,
		"usage: /session <name>",
		"usage: /session new",
	}

	names, err := session.ListSessionNames(r.cfg.StateDir)
	if err != nil || len(names) == 0 {
		return strings.Join(lines, "\n")
	}

	if len(names) > 8 {
		names = names[:8]
	}
	lines = append(lines, "recent="+strings.Join(names, ", "))
	return strings.Join(lines, "\n")
}

func (r *chatRuntime) executeTask(ctx context.Context, task string) (string, error) {
	_ = session.AppendTranscript(r.transcriptPath, "user", task)
	result, err := r.session.RunTask(ctx, task)
	r.dirtySession = true
	if err != nil {
		_ = session.AppendTranscript(r.transcriptPath, "error", err.Error())
		return "", err
	}

	output := formatRunOutput(result.Final, r.outputMode)
	_ = session.AppendTranscript(r.transcriptPath, "assistant", output)
	_ = r.save()
	return output, nil
}

func (r *chatRuntime) newSession() *agent.Session {
	return r.loop.NewSessionWithMemory(memory.RenderSystemMemory(r.globalMemory, r.projectMemory))
}

func (r *chatRuntime) refreshMemoryContext() {
	messages := r.session.Messages()
	if len(messages) == 0 {
		r.session = r.newSession()
		return
	}
	messages[0].Content = agent.SystemPromptWithMemory(memory.RenderSystemMemory(r.globalMemory, r.projectMemory))
	r.session.ReplaceMessages(messages)
}

func (r *chatRuntime) save() error {
	r.cfg.ApprovalMode = r.approver.Mode()
	return session.Save(r.statePath, session.State{
		SessionName:  r.sessionName,
		Model:        r.cfg.Model,
		OutputMode:   r.outputMode,
		ApprovalMode: r.approver.Mode(),
		Messages:     r.session.Messages(),
	})
}

func (r *chatRuntime) saveMemory() error {
	state := memory.State{
		Global: r.globalMemory,
		Projects: map[string][]string{
			r.scopeKey: r.projectMemory,
		},
	}
	if existing, err := memory.Load(r.memoryPath); err == nil {
		state.Global = memory.Normalize(append(existing.Global, r.globalMemory...))
		state.Projects = existing.Projects
		if state.Projects == nil {
			state.Projects = make(map[string][]string)
		}
		state.Projects[r.scopeKey] = r.projectMemory
	}
	return memory.Save(r.memoryPath, state)
}

func (r *chatRuntime) beforeExit() {
	if r.autoMemoryExit && r.dirtySession {
		if added, err := r.summarizeMemory(); err != nil {
			fmt.Fprintf(os.Stderr, "auto-memory error: %v\n", err)
		} else if added > 0 {
			fmt.Fprintf(os.Stderr, "auto-memorized %d note(s)\n", added)
		}
	}
	_ = r.save()
}

func (r *chatRuntime) summarizeMemory() (int, error) {
	lines, err := r.collectMemoryNotes()
	if err != nil {
		return 0, err
	}

	before := len(r.projectMemory)
	for _, line := range lines {
		r.projectMemory = memory.Add(r.projectMemory, line)
	}
	added := len(r.projectMemory) - before
	r.refreshMemoryContext()
	if added > 0 {
		if err := r.saveMemory(); err != nil {
			return 0, err
		}
	}
	if err := r.save(); err != nil {
		return 0, err
	}
	r.dirtySession = false
	return added, nil
}

func (r *chatRuntime) collectMemoryNotes() ([]string, error) {
	text := buildConversationSummaryInput(r.session.Messages())
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}

	lines, err := r.collectMemoryNotesWithModel(text)
	if len(lines) > 0 {
		return lines, nil
	}

	fallback := extractStableMemoryNotes(r.session.Messages())
	if len(fallback) > 0 {
		return fallback, nil
	}

	if err != nil {
		return nil, err
	}
	return nil, nil
}

func (r *chatRuntime) collectMemoryNotesWithModel(text string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), autoMemoryTimeout)
	defer cancel()

	client := openaiapi.NewClient(r.cfg.BaseURL, r.cfg.Model, r.cfg.APIKey)
	resp, err := client.Complete(ctx, model.Request{
		Model: r.cfg.Model,
		Messages: []model.Message{
			{
				Role:    "system",
				Content: "Extract only stable reusable memory notes from this conversation. Focus on user preferences, stable project facts, workflow constraints, or recurring expectations. Ignore transient task results. Return 1 to 5 plain bullet lines and nothing else.",
			},
			{
				Role:    "user",
				Content: text,
			},
		},
		Temperature: 0,
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("memory summarizer returned no choices")
	}

	return parseMemoryLines(modelContent(resp.Choices[0].Message.Content)), nil
}

func buildConversationSummaryInput(messages []model.Message) string {
	var entries []string
	var b strings.Builder
	for _, msg := range messages {
		switch msg.Role {
		case "user", "assistant":
			text := strings.TrimSpace(agent.FormatTerminalOutput(modelContent(msg.Content)))
			if text == "" {
				continue
			}
			limit := memorySummaryEntryChars
			if msg.Role == "assistant" {
				limit = memorySummaryEntryChars / 2
			}
			text = truncateSummaryEntry(text, limit)
			entries = append(entries, msg.Role+": "+text)
		}
	}

	if len(entries) > memorySummaryMaxMessages {
		entries = entries[len(entries)-memorySummaryMaxMessages:]
	}
	for len(entries) > 1 && joinedLength(entries, "\n\n") > memorySummaryMaxChars {
		entries = entries[1:]
	}
	for i, entry := range entries {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(entry)
	}
	return b.String()
}

func truncateSummaryEntry(text string, limit int) string {
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.Join(strings.Fields(text), " ")
	if limit > 0 && len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}

func parseMemoryLines(text string) []string {
	text = agent.FormatTerminalOutput(text)
	lines := strings.Split(text, "\n")
	var out []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return memory.Normalize(out)
}

func extractStableMemoryNotes(messages []model.Message) []string {
	var out []string
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		for _, part := range memoryCandidateSplit.Split(modelContent(msg.Content), -1) {
			if note, ok := normalizeStableMemoryNote(part); ok {
				out = append(out, note)
			}
		}
	}
	return memory.Normalize(out)
}

func normalizeStableMemoryNote(text string) (string, bool) {
	text = strings.TrimSpace(text)
	text = strings.Trim(text, "-* \t\"'`")
	text = strings.Join(strings.Fields(text), " ")
	if text == "" || len(text) < 8 || len(text) > 180 {
		return "", false
	}
	if strings.HasPrefix(text, "/") {
		return "", false
	}

	lower := strings.ToLower(text)
	if strings.HasSuffix(text, "?") || strings.HasSuffix(text, "？") {
		return "", false
	}
	if isPreferenceMemory(lower) || isProjectMemory(lower) {
		return rewriteMemoryNote(text, lower), true
	}
	return "", false
}

func isPreferenceMemory(lower string) bool {
	return hasAny(lower,
		"my preference",
		"i prefer",
		"default to",
		"always answer",
		"always respond",
		"keep answers concise",
		"keep answers brief",
		"keep responses concise",
		"keep responses brief",
		"answer in chinese",
		"answer in english",
		"respond in chinese",
		"respond in english",
		"prefer concise",
		"prefer brief",
		"prefer short",
		"prefer plain text",
		"prefer markdown",
		"中文回答",
		"英文回答",
		"输出中文",
		"输出英文",
		"默认中文",
		"默认英文",
		"简洁回答",
		"简短回答",
		"偏好",
		"默认",
		"尽量简洁",
		"尽量简短",
	)
}

func isProjectMemory(lower string) bool {
	return hasAny(lower,
		"this repo",
		"this project",
		"this workspace",
		"the repo",
		"the project",
		"the workspace",
		"repo uses",
		"project uses",
		"workspace uses",
		"codebase",
		"这个仓库",
		"这个项目",
		"这个工作区",
		"当前仓库",
		"当前项目",
		"当前工作区",
		"该仓库",
		"该项目",
		"代码库",
		"工作区",
	)
}

func rewriteMemoryNote(text, lower string) string {
	replacements := []struct {
		prefix string
		out    string
	}{
		{prefix: "my preference is ", out: "Prefer "},
		{prefix: "i prefer ", out: "Prefer "},
		{prefix: "please answer in ", out: "Answer in "},
		{prefix: "please respond in ", out: "Respond in "},
		{prefix: "please keep answers ", out: "Keep answers "},
		{prefix: "please keep responses ", out: "Keep responses "},
	}

	for _, item := range replacements {
		if strings.HasPrefix(lower, item.prefix) && len(text) >= len(item.prefix) {
			return item.out + strings.TrimSpace(text[len(item.prefix):])
		}
	}
	return text
}

func hasAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func joinedLength(parts []string, sep string) int {
	if len(parts) == 0 {
		return 0
	}
	total := len(sep) * (len(parts) - 1)
	for _, part := range parts {
		total += len(part)
	}
	return total
}

func pingModel(args []string) int {
	cfg := config.FromEnv()

	fs := flag.NewFlagSet("ping", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.BaseURL, "base-url", cfg.BaseURL, "OpenAI-compatible API base URL")
	fs.StringVar(&cfg.Model, "model", cfg.Model, "model name")
	fs.StringVar(&cfg.APIKey, "api-key", cfg.APIKey, "optional API key")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "invalid config: %v\n", err)
		return 2
	}

	client := openaiapi.NewClient(cfg.BaseURL, cfg.Model, cfg.APIKey)
	if _, err := client.Models(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "model API error: %v\n", err)
		return 1
	}

	resp, err := client.Complete(context.Background(), openaiapi.PingRequest(cfg.Model))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ping failed: %v\n", err)
		return 1
	}
	if len(resp.Choices) == 0 {
		fmt.Fprintln(os.Stderr, "ping failed: no choices returned")
		return 1
	}

	fmt.Println(lastNonEmptyLine(agent.FormatTerminalOutput(modelContent(resp.Choices[0].Message.Content))))
	return 0
}

func listModels(args []string) int {
	cfg := config.FromEnv()

	fs := flag.NewFlagSet("models", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.BaseURL, "base-url", cfg.BaseURL, "OpenAI-compatible API base URL")
	fs.StringVar(&cfg.APIKey, "api-key", cfg.APIKey, "optional API key")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "invalid config: %v\n", err)
		return 2
	}

	client := openaiapi.NewClient(cfg.BaseURL, cfg.Model, cfg.APIKey)
	models, err := client.Models(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "model API error: %v\n", err)
		return 1
	}

	if len(models) == 0 {
		fmt.Println("(no models returned)")
		return 0
	}

	sort.Strings(models)
	for _, name := range models {
		fmt.Println(name)
	}
	return 0
}

func parseAgentFlags(name string, args []string) (config.Config, runtimeOptions, []string, *bufio.Reader, error) {
	cfg := config.FromEnv()
	opts := runtimeOptions{
		outputMode:     defaultOutputMode(name),
		autoMemoryExit: name == "chat",
	}
	dangerously := false

	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.BaseURL, "base-url", cfg.BaseURL, "OpenAI-compatible API base URL")
	fs.StringVar(&cfg.Model, "model", cfg.Model, "model name")
	fs.StringVar(&cfg.APIKey, "api-key", cfg.APIKey, "optional API key")
	fs.StringVar(&cfg.WorkDir, "workdir", cfg.WorkDir, "workspace root")
	switch name {
	case "run":
		fs.BoolVar(&dangerously, "dangerously", false, "skip command and file-write approval for this run")
	case "chat":
		fs.BoolVar(&dangerously, "dangerously", false, "start chat in dangerously mode")
		fs.StringVar(&opts.session, "session", "", "chat session name to resume or create")
	}

	if err := fs.Parse(args); err != nil {
		return cfg, runtimeOptions{}, nil, nil, err
	}
	if dangerously {
		cfg.ApprovalMode = tools.ApprovalDangerously
	}
	if err := cfg.Validate(); err != nil {
		return cfg, runtimeOptions{}, nil, nil, err
	}

	cfg.ApprovalMode = strings.ToLower(strings.TrimSpace(cfg.ApprovalMode))
	switch cfg.ApprovalMode {
	case "", tools.ApprovalConfirm:
		cfg.ApprovalMode = tools.ApprovalConfirm
	case tools.ApprovalDangerously:
	default:
		return cfg, runtimeOptions{}, nil, nil, fmt.Errorf("invalid approval mode %q", cfg.ApprovalMode)
	}

	return cfg, opts, fs.Args(), bufio.NewReader(os.Stdin), nil
}

func defaultOutputMode(name string) string {
	if name == "chat" {
		return "terminal"
	}
	return "raw"
}

func resolveChatSessionName(name string, now time.Time) string {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "new") {
		return defaultChatSessionName(now)
	}
	return name
}

func defaultChatSessionName(now time.Time) string {
	return "chat-" + now.Local().Format("20060102-150405")
}

func buildAgent(cfg config.Config, reader *bufio.Reader) (*agent.Agent, tools.Approver) {
	interactive := tools.IsInteractiveTerminal(os.Stdin)
	approver := tools.NewTerminalApprover(reader, os.Stderr, cfg.ApprovalMode, interactive)
	return buildAgentWith(cfg, approver, os.Stderr), approver
}

func buildAgentWith(cfg config.Config, approver tools.Approver, log io.Writer) *agent.Agent {
	client := openaiapi.NewClient(cfg.BaseURL, cfg.Model, cfg.APIKey)
	registry := tools.NewRegistry(cfg.WorkDir, cfg.Shell, cfg.CommandTimeout, approver)
	return agent.New(client, registry, cfg.MaxSteps, log)
}

func modelContent(content any) string {
	return openaiapi.ContentString(content)
}

func lastNonEmptyLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line
		}
	}
	return ""
}

func formatRunOutput(text, mode string) string {
	switch mode {
	case "terminal":
		return agent.FormatTerminalOutput(text)
	default:
		return strings.TrimSpace(text)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  onek                 # default chat on interactive terminals")
	fmt.Fprintln(os.Stderr, "  onek -d              # default chat in dangerously mode")
	fmt.Fprintln(os.Stderr, "  onek chat")
	fmt.Fprintln(os.Stderr, "  onek run [--dangerously] <task>")
	fmt.Fprintln(os.Stderr, "  onek <task>          # shorthand for run")
	fmt.Fprintln(os.Stderr, "  onek ping [flags]")
	fmt.Fprintln(os.Stderr, "  onek models [flags]")
	fmt.Fprintln(os.Stderr, "  onek version")
	fmt.Fprintln(os.Stderr)
	printRunUsage()
}

func printRunUsage() {
	fmt.Fprintln(os.Stderr, "Examples:")
	fmt.Fprintln(os.Stderr, `  onek`)
	fmt.Fprintln(os.Stderr, `  onek -d`)
	fmt.Fprintln(os.Stderr, `  onek "inspect this repo"`)
	fmt.Fprintln(os.Stderr, `  onek -d "run go test ./..."`)
	fmt.Fprintln(os.Stderr, `  onek run --dangerously "run go test ./..."`)
	fmt.Fprintln(os.Stderr, `  onek chat`)
	fmt.Fprintln(os.Stderr, `  onek chat --session bugfix`)
}
