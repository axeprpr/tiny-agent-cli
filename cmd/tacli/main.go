package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"tiny-agent-cli/internal/agent"
	"tiny-agent-cli/internal/config"
	"tiny-agent-cli/internal/harness"
	"tiny-agent-cli/internal/i18n"
	"tiny-agent-cli/internal/memory"
	"tiny-agent-cli/internal/model"
	"tiny-agent-cli/internal/model/openaiapi"
	"tiny-agent-cli/internal/plugins"
	"tiny-agent-cli/internal/session"
	"tiny-agent-cli/internal/tools"
	"tiny-agent-cli/internal/trace"
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
	jobs           *jobManager
	sessionName    string
	outputMode     string
	transcriptPath string
	statePath      string
	memoryPath     string
	auditPath      string
	tracePath      string
	scopeKey       string
	globalMemory   []string
	projectMemory  []string
	autoMemoryExit bool
	dirtySession   bool
	tracer         *trace.FileSink
	pluginManager  *plugins.Manager
	pluginCommands map[string]plugins.Command
	skills         []tools.Skill
}

type runtimeAgentEventSink struct {
	runtime *chatRuntime
}

func (s runtimeAgentEventSink) RecordAgentEvent(ctx context.Context, event agent.AgentEvent) {
	if s.runtime == nil {
		return
	}
	data := cloneAnyMap(event.Data)
	s.runtime.recordTrace(ctx, "agent", event.Type, data)
}

type memoryIntent struct {
	action string
	scope  string
	body   string
}

const (
	memorySummaryMaxMessages = 24
	memorySummaryMaxChars    = 8000
	memorySummaryEntryChars  = 320
	autoMemoryTimeout        = 20 * time.Second
	autoExploreMinChars      = 48
)

const (
	memoryActionRemember = "remember"
	memoryActionForget   = "forget"
	memoryActionShow     = "show"
	memoryScopeProject   = "project"
	memoryScopeGlobal    = "global"
	memoryScopeAll       = "all"
)

var memoryCandidateSplit = regexp.MustCompile(`[\n\r.!?。！？;；]+`)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	cfg := config.FromEnv()
	initLanguage(cfg.StateDir, startupMode(args, tools.IsInteractiveTerminal(os.Stdin)))
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

func startupMode(args []string, interactive bool) string {
	args, _ = peelGlobalDangerously(args)
	if len(args) == 0 {
		if interactive {
			return "chat"
		}
		return "run"
	}
	switch args[0] {
	case "chat":
		return "chat"
	default:
		return "run"
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
					runtime.beforeExit(true)
					return result.exitCode
				}
				if readErr != nil {
					runtime.beforeExit(true)
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
			runtime.beforeExit(true)
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
		auditPath:      tools.AuditPath(cfg.StateDir),
		scopeKey:       memory.ScopeKey(cfg.WorkDir),
		pluginCommands: make(map[string]plugins.Command),
	}
	r.setSessionName(sessionName)
	r.attachAgentEventSink()
	if mem, err := memory.Load(r.memoryPath); err == nil {
		r.globalMemory = mem.Global
		r.projectMemory = mem.Projects[r.scopeKey]
	}
	r.session = r.newSession()
	r.jobs = newJobManager(cfg, memory.RenderSystemMemory(r.globalMemory, r.projectMemory))
	r.jobs.SetRoleRouter(llmBackgroundRoleRouter(cfg))
	if discovered, err := tools.DiscoverSkills(cfg.WorkDir); err == nil {
		r.skills = discovered
	}
	if manager, err := plugins.NewManager(); err == nil {
		r.pluginManager = manager
		_, _ = manager.Discover()
	}

	if strings.TrimSpace(opts.session) != "" {
		if _, err := r.loadSessionState(); err != nil {
			return nil, err
		}
	}
	r.recordTrace(context.Background(), "runtime", "runtime_started", map[string]any{
		"model":      cfg.Model,
		"workdir":    cfg.WorkDir,
		"session":    r.sessionName,
		"trace_path": r.tracePath,
	})

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
	r.recordTrace(context.Background(), "runtime", "command", map[string]any{
		"name": fields[0],
		"raw":  strings.TrimSpace(input),
	})

	switch fields[0] {
	case "/exit", "/quit":
		return runtimeCommandResult{handled: true, exitCode: 0}
	case "/reset":
		r.session = r.newSession()
		_ = r.save()
		return runtimeCommandResult{handled: true, output: i18n.T("cmd.reset"), exitCode: -1}
	case "/help":
		return runtimeCommandResult{handled: true, output: i18n.T("help"), exitCode: -1}
	case "/status":
		jobSummary := "jobs=0"
		if r.jobs != nil {
			jobSummary = r.jobs.Summary()
		}
		auditSummary := r.auditStatusLine()
		traceSummary := r.traceStatusLine()
		pluginSummary := "plugins=0/0"
		if r.pluginManager != nil {
			pluginSummary = fmt.Sprintf("plugins=%d/%d", len(r.pluginManager.Loaded()), len(r.pluginManager.List()))
		}
		return runtimeCommandResult{handled: true, output: fmt.Sprintf("session=%s\nmemory_scope=%s\nglobal_memory_notes=%d\nproject_memory_notes=%d\nstate=%s\ntranscript=%s\nmemory=%s\naudit=%s\ntrace=%s\n%s\n%s",
			r.sessionName, r.scopeKey, len(r.globalMemory), len(r.projectMemory), r.statePath, r.transcriptPath, r.memoryPath, auditSummary, traceSummary, jobSummary, pluginSummary), exitCode: -1}
	case "/plan":
		return runtimeCommandResult{handled: true, output: r.planCommand(), exitCode: -1}
	case "/compact":
		if r.session.Compact() {
			r.dirtySession = true
			_ = r.save()
			return runtimeCommandResult{handled: true, output: "conversation compacted", exitCode: -1}
		}
		return runtimeCommandResult{handled: true, output: "conversation did not need compaction", exitCode: -1}
	case "/hooks":
		return runtimeCommandResult{handled: true, output: r.hooksCommand(fields), exitCode: -1}
	case "/plugin":
		return runtimeCommandResult{handled: true, output: r.pluginCommand(fields, input), exitCode: -1}
	case "/skills":
		return runtimeCommandResult{handled: true, output: r.skillsCommand(), exitCode: -1}
	case "/audit":
		return runtimeCommandResult{handled: true, output: r.auditCommand(fields), exitCode: -1}
	case "/trace":
		return runtimeCommandResult{handled: true, output: r.traceCommand(fields), exitCode: -1}
	case "/session":
		if len(fields) == 1 {
			return runtimeCommandResult{handled: true, output: r.describeSessions(), exitCode: -1}
		}
		if action := strings.ToLower(strings.TrimSpace(fields[1])); action == "save" {
			if err := r.save(); err != nil {
				return runtimeCommandResult{handled: true, output: fmt.Sprintf("session save error: %v", err), exitCode: -1}
			}
			return runtimeCommandResult{handled: true, output: "session saved", exitCode: -1}
		}
		if action := strings.ToLower(strings.TrimSpace(fields[1])); action == "restore" || action == "load" {
			loaded, err := r.loadSessionState()
			if err != nil {
				return runtimeCommandResult{handled: true, output: fmt.Sprintf("session restore error: %v", err), exitCode: -1}
			}
			if !loaded {
				return runtimeCommandResult{handled: true, output: "no saved state for current session", exitCode: -1}
			}
			return runtimeCommandResult{handled: true, output: "session restored", exitCode: -1}
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
			return runtimeCommandResult{handled: true, output: i18n.T("cmd.approval.usage"), exitCode: -1}
		}
		if err := r.approver.SetMode(fields[1]); err != nil {
			return runtimeCommandResult{handled: true, output: err.Error(), exitCode: -1}
		}
		r.cfg.ApprovalMode = r.approver.Mode()
		_ = r.save()
		return runtimeCommandResult{handled: true, output: fmt.Sprintf(i18n.T("cmd.approval.set"), r.approver.Mode()), exitCode: -1}
	case "/output":
		return runtimeCommandResult{handled: true, output: i18n.T("cmd.output.deprecated"), exitCode: -1}
	case "/model":
		if len(fields) < 2 {
			return runtimeCommandResult{handled: true, output: i18n.T("cmd.model.usage"), exitCode: -1}
		}
		r.cfg.Model = strings.Join(fields[1:], " ")
		r.rebuildLoop()
		return runtimeCommandResult{handled: true, output: fmt.Sprintf(i18n.T("cmd.model.set"), r.cfg.Model), exitCode: -1}
	case "/bg":
		id, err := r.jobs.Start(strings.TrimSpace(input[len("/bg"):]))
		if err != nil {
			return runtimeCommandResult{handled: true, output: err.Error(), exitCode: -1}
		}
		role := backgroundRoleGeneral
		if snap, ok := r.jobs.Snapshot(id); ok {
			role = normalizeBackgroundRole(snap.Role)
		}
		return runtimeCommandResult{handled: true, output: fmt.Sprintf("%s (role=%s)", fmt.Sprintf(i18n.T("cmd.bg.started"), id), role), exitCode: -1}
	case "/bg-role":
		if len(fields) < 3 {
			return runtimeCommandResult{handled: true, output: i18n.T("cmd.bgrole.usage"), exitCode: -1}
		}
		role := strings.TrimSpace(fields[1])
		task := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), fields[0]+" "+fields[1]))
		id, err := r.jobs.StartWithRole(role, task)
		if err != nil {
			return runtimeCommandResult{handled: true, output: err.Error(), exitCode: -1}
		}
		normalized := normalizeBackgroundRole(role)
		if snap, ok := r.jobs.Snapshot(id); ok {
			normalized = normalizeBackgroundRole(snap.Role)
		}
		return runtimeCommandResult{handled: true, output: fmt.Sprintf("%s (role=%s)", fmt.Sprintf(i18n.T("cmd.bg.started"), id), normalized), exitCode: -1}
	case "/jobs":
		return runtimeCommandResult{handled: true, output: formatJobList(r.jobs.List()), exitCode: -1}
	case "/job":
		if len(fields) != 2 {
			return runtimeCommandResult{handled: true, output: i18n.T("cmd.job.usage"), exitCode: -1}
		}
		snap, ok := r.jobs.Snapshot(fields[1])
		if !ok {
			return runtimeCommandResult{handled: true, output: fmt.Sprintf(i18n.T("cmd.job.unknown"), fields[1]), exitCode: -1}
		}
		return runtimeCommandResult{handled: true, output: formatJobSnapshot(snap), exitCode: -1}
	case "/job-send":
		if len(fields) < 3 {
			return runtimeCommandResult{handled: true, output: i18n.T("cmd.jobsend.usage"), exitCode: -1}
		}
		body := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), fields[0]+" "+fields[1]))
		if err := r.jobs.Send(fields[1], body); err != nil {
			return runtimeCommandResult{handled: true, output: err.Error(), exitCode: -1}
		}
		return runtimeCommandResult{handled: true, output: fmt.Sprintf(i18n.T("cmd.jobsend.ok"), fields[1]), exitCode: -1}
	case "/job-cancel":
		if len(fields) != 2 {
			return runtimeCommandResult{handled: true, output: i18n.T("cmd.jobcancel.usage"), exitCode: -1}
		}
		if err := r.jobs.Cancel(fields[1]); err != nil {
			return runtimeCommandResult{handled: true, output: err.Error(), exitCode: -1}
		}
		return runtimeCommandResult{handled: true, output: fmt.Sprintf(i18n.T("cmd.jobcancel.ok"), fields[1]), exitCode: -1}
	case "/job-apply":
		if len(fields) != 2 {
			return runtimeCommandResult{handled: true, output: i18n.T("cmd.jobapply.usage"), exitCode: -1}
		}
		snap, ok := r.jobs.Snapshot(fields[1])
		if !ok {
			return runtimeCommandResult{handled: true, output: fmt.Sprintf(i18n.T("cmd.job.unknown"), fields[1]), exitCode: -1}
		}
		r.injectJobSummary(snap)
		_ = r.save()
		return runtimeCommandResult{handled: true, output: fmt.Sprintf(i18n.T("cmd.jobapply.ok"), fields[1]), exitCode: -1}
	case "/memory":
		return runtimeCommandResult{handled: true, output: r.memoryCommand(fields, input), exitCode: -1}
	case "/remember":
		if len(fields) < 2 {
			return runtimeCommandResult{handled: true, output: i18n.T("cmd.remember.usage"), exitCode: -1}
		}
		r.projectMemory = memory.Add(r.projectMemory, strings.TrimSpace(input[len("/remember"):]))
		r.refreshMemoryContext()
		_ = r.saveMemory()
		_ = r.save()
		return runtimeCommandResult{handled: true, output: i18n.T("cmd.remember.ok"), exitCode: -1}
	case "/remember-global":
		if len(fields) < 2 {
			return runtimeCommandResult{handled: true, output: i18n.T("cmd.rememberg.usage"), exitCode: -1}
		}
		r.globalMemory = memory.Add(r.globalMemory, strings.TrimSpace(input[len("/remember-global"):]))
		r.refreshMemoryContext()
		_ = r.saveMemory()
		_ = r.save()
		return runtimeCommandResult{handled: true, output: i18n.T("cmd.rememberg.ok"), exitCode: -1}
	case "/forget":
		if len(fields) < 2 {
			return runtimeCommandResult{handled: true, output: i18n.T("cmd.forget.usage"), exitCode: -1}
		}
		updated, removed := memory.ForgetMatching(r.projectMemory, strings.TrimSpace(input[len("/forget"):]))
		r.projectMemory = updated
		r.refreshMemoryContext()
		_ = r.saveMemory()
		_ = r.save()
		return runtimeCommandResult{handled: true, output: fmt.Sprintf(i18n.T("cmd.forget.ok"), removed), exitCode: -1}
	case "/forget-global":
		if len(fields) < 2 {
			return runtimeCommandResult{handled: true, output: i18n.T("cmd.forgetg.usage"), exitCode: -1}
		}
		updated, removed := memory.ForgetMatching(r.globalMemory, strings.TrimSpace(input[len("/forget-global"):]))
		r.globalMemory = updated
		r.refreshMemoryContext()
		_ = r.saveMemory()
		_ = r.save()
		return runtimeCommandResult{handled: true, output: fmt.Sprintf(i18n.T("cmd.forgetg.ok"), removed), exitCode: -1}
	case "/memorize":
		added, err := r.summarizeMemory()
		if err != nil {
			return runtimeCommandResult{handled: true, output: fmt.Sprintf(i18n.T("cmd.memorize.err"), err), exitCode: -1}
		}
		return runtimeCommandResult{handled: true, output: fmt.Sprintf(i18n.T("cmd.memorize.ok"), added), exitCode: -1}
	default:
		if output, ok, err := r.runPluginCommand(fields, input); ok {
			if err != nil {
				return runtimeCommandResult{handled: true, output: err.Error(), exitCode: -1}
			}
			return runtimeCommandResult{handled: true, output: output, exitCode: -1}
		}
		return runtimeCommandResult{handled: false, exitCode: -1}
	}
}

func (r *chatRuntime) rebuildLoop() {
	var jobs tools.JobControl
	if r.jobs != nil {
		jobs = jobToolAdapter{manager: r.jobs}
	}
	r.loop = buildAgentWith(r.cfg, r.approver, os.Stderr, jobs)
	r.attachAgentEventSink()
	r.applyLoadedPlugins()
	r.session.SetAgent(r.loop)
	_ = r.approver.SetMode(r.cfg.ApprovalMode)
	if r.jobs != nil {
		r.jobs.UpdateConfig(r.cfg)
	}
}

func (r *chatRuntime) setSessionName(name string) {
	r.sessionName = strings.TrimSpace(name)
	r.transcriptPath = session.TranscriptPath(r.cfg.StateDir, r.sessionName)
	r.statePath = session.SessionPath(r.cfg.StateDir, r.sessionName)
	r.tracePath = trace.Path(r.cfg.StateDir, r.sessionName)
	r.tracer = trace.NewFileSink(r.tracePath)
}

func (r *chatRuntime) loadSessionState() (bool, error) {
	state, err := session.Load(r.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	if len(state.Messages) > 0 {
		r.session.ReplaceMessages(state.Messages)
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
		r.recordTrace(context.Background(), "runtime", "session_switched", map[string]any{"session": r.sessionName, "loaded": true})
		return fmt.Sprintf("switched to session %s", r.sessionName), nil
	}
	r.recordTrace(context.Background(), "runtime", "session_switched", map[string]any{"session": r.sessionName, "loaded": false})
	return fmt.Sprintf("started session %s", r.sessionName), nil
}

func (r *chatRuntime) describeSessions() string {
	lines := []string{
		"current=" + r.sessionName,
		"usage: /session <name>",
		"usage: /session new",
		"usage: /session save",
		"usage: /session restore",
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

func (r *chatRuntime) planCommand() string {
	path := filepath.Join(r.cfg.WorkDir, "docs", "plan.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("plan read error: %v", err)
	}
	return strings.TrimSpace(string(data))
}

func (r *chatRuntime) hooksCommand(fields []string) string {
	if len(fields) == 1 {
		disabled := "(none)"
		if len(r.cfg.Hooks.Disabled) > 0 {
			disabled = strings.Join(r.cfg.Hooks.Disabled, ", ")
		}
		return fmt.Sprintf("enabled=%t\ndisabled=%s", r.cfg.Hooks.Enabled, disabled)
	}
	switch strings.ToLower(strings.TrimSpace(fields[1])) {
	case "enable":
		r.cfg.Hooks.Enabled = true
	case "disable":
		r.cfg.Hooks.Enabled = false
	case "disable-hook":
		if len(fields) < 3 {
			return "usage: /hooks disable-hook <name>"
		}
		r.cfg.Hooks.Disabled = append(r.cfg.Hooks.Disabled, fields[2])
	case "enable-hook":
		if len(fields) < 3 {
			return "usage: /hooks enable-hook <name>"
		}
		target := strings.ToLower(strings.TrimSpace(fields[2]))
		filtered := make([]string, 0, len(r.cfg.Hooks.Disabled))
		for _, item := range r.cfg.Hooks.Disabled {
			if strings.ToLower(strings.TrimSpace(item)) != target {
				filtered = append(filtered, item)
			}
		}
		r.cfg.Hooks.Disabled = filtered
	default:
		return "usage: /hooks [enable|disable|disable-hook <name>|enable-hook <name>]"
	}
	r.rebuildLoop()
	_ = r.save()
	return r.hooksCommand([]string{"/hooks"})
}

func (r *chatRuntime) skillsCommand() string {
	if len(r.skills) == 0 {
		return "no skills discovered"
	}
	lines := make([]string, 0, len(r.skills))
	for _, skill := range r.skills {
		line := skill.Name + " [" + firstNonEmpty(skill.Source, "local") + "]"
		if strings.TrimSpace(skill.Description) != "" {
			line += ": " + skill.Description
		}
		if len(skill.ToolDefinitions) > 0 {
			line += " tools=" + strings.Join(skill.ToolDefinitions, ",")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (r *chatRuntime) pluginCommand(fields []string, raw string) string {
	if r.pluginManager == nil {
		return "plugin manager unavailable"
	}
	if len(fields) == 1 || strings.EqualFold(fields[1], "list") {
		_, _ = r.pluginManager.Discover()
		discovered := r.pluginManager.List()
		if len(discovered) == 0 {
			return "no plugins discovered"
		}
		loaded := make(map[string]bool)
		for _, item := range r.pluginManager.Loaded() {
			loaded[item.Descriptor.Path] = true
		}
		lines := make([]string, 0, len(discovered))
		for _, item := range discovered {
			status := "discovered"
			if loaded[item.Path] {
				status = "loaded"
			}
			lines = append(lines, fmt.Sprintf("%s [%s] %s", item.Name, status, item.Path))
		}
		return strings.Join(lines, "\n")
	}
	if !strings.EqualFold(fields[1], "load") {
		return "usage: /plugin [list|load <name-or-path>]"
	}
	if len(fields) < 3 {
		return "usage: /plugin load <name-or-path>"
	}
	target := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), fields[0]+" "+fields[1]))
	loaded, err := r.pluginManager.Load(target)
	if err != nil {
		return "plugin load error: " + err.Error()
	}
	r.rebuildLoop()
	meta := loaded.Plugin.Metadata()
	if strings.TrimSpace(meta.Name) == "" {
		meta.Name = loaded.Descriptor.Name
	}
	return fmt.Sprintf("loaded plugin %s", meta.Name)
}

func (r *chatRuntime) runPluginCommand(fields []string, raw string) (string, bool, error) {
	if len(fields) == 0 || r.pluginCommands == nil {
		return "", false, nil
	}
	cmd, ok := r.pluginCommands[fields[0]]
	if !ok {
		return "", false, nil
	}
	output, err := cmd.Handler(context.Background(), fields[1:], raw)
	return output, true, err
}

func (r *chatRuntime) applyLoadedPlugins() {
	if r.pluginManager == nil {
		return
	}
	r.pluginCommands = make(map[string]plugins.Command)
	for _, loaded := range r.pluginManager.Loaded() {
		r.applyPlugin(loaded)
	}
}

func (r *chatRuntime) applyPlugin(loaded plugins.Loaded) {
	for _, tool := range loaded.Plugin.Tools() {
		r.loop.AddTool(tool)
	}
	for _, hook := range loaded.Plugin.Hooks() {
		r.loop.AddHook(hook)
	}
	for _, command := range loaded.Plugin.Commands() {
		name := strings.TrimSpace(command.Name)
		if name == "" {
			continue
		}
		if !strings.HasPrefix(name, "/") {
			name = "/" + name
		}
		command.Name = name
		r.pluginCommands[name] = command
	}
}

func (r *chatRuntime) memoryCommand(fields []string, input string) string {
	if len(fields) == 1 || strings.EqualFold(fields[1], "show") {
		return memory.FormatNotes(r.globalMemory, r.projectMemory)
	}
	switch strings.ToLower(strings.TrimSpace(fields[1])) {
	case "remember":
		if len(fields) < 3 {
			return "usage: /memory remember <text>"
		}
		r.projectMemory = memory.Add(r.projectMemory, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), fields[0]+" "+fields[1])))
	case "remember-global":
		if len(fields) < 3 {
			return "usage: /memory remember-global <text>"
		}
		r.globalMemory = memory.Add(r.globalMemory, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), fields[0]+" "+fields[1])))
	case "forget":
		if len(fields) < 3 {
			return "usage: /memory forget <query>"
		}
		var removed int
		r.projectMemory, removed = memory.ForgetMatching(r.projectMemory, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), fields[0]+" "+fields[1])))
		r.refreshMemoryContext()
		_ = r.saveMemory()
		_ = r.save()
		return fmt.Sprintf("removed %d project memory note(s)", removed)
	case "forget-global":
		if len(fields) < 3 {
			return "usage: /memory forget-global <query>"
		}
		var removed int
		r.globalMemory, removed = memory.ForgetMatching(r.globalMemory, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), fields[0]+" "+fields[1])))
		r.refreshMemoryContext()
		_ = r.saveMemory()
		_ = r.save()
		return fmt.Sprintf("removed %d global memory note(s)", removed)
	default:
		return "usage: /memory [show|remember <text>|remember-global <text>|forget <query>|forget-global <query>]"
	}
	r.refreshMemoryContext()
	_ = r.saveMemory()
	_ = r.save()
	return memory.FormatNotes(r.globalMemory, r.projectMemory)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (r *chatRuntime) executeTask(ctx context.Context, task string) (string, error) {
	return r.executeTaskStreaming(ctx, task, nil)
}

func (r *chatRuntime) executeTaskStreaming(ctx context.Context, task string, onToken func(string)) (string, error) {
	if handled, output, err := r.tryHandleNaturalLanguageMemory(task); handled {
		return output, err
	}
	r.maybeApplyReadyJobSummaries()
	r.maybeStartAutoExplore(task)
	r.recordTrace(ctx, "runtime", "task_start", map[string]any{
		"task_chars": len(strings.TrimSpace(task)),
		"streaming":  onToken != nil && r.loop.CanStream(),
	})

	_ = session.AppendTranscript(r.transcriptPath, "user", task)
	var result agent.Result
	var err error
	if onToken != nil && r.loop.CanStream() {
		result, err = r.session.RunTaskStreaming(ctx, task, onToken)
	} else {
		result, err = r.session.RunTask(ctx, task)
	}
	r.dirtySession = true
	if err != nil {
		_ = session.AppendTranscript(r.transcriptPath, "error", err.Error())
		r.recordTrace(ctx, "runtime", "task_error", map[string]any{
			"error": err.Error(),
		})
		return "", err
	}

	output := formatRunOutput(result.Final, r.outputMode)
	_ = session.AppendTranscript(r.transcriptPath, "assistant", output)
	_ = r.save()
	r.recordTrace(ctx, "runtime", "task_finish", map[string]any{
		"output_chars": len(strings.TrimSpace(output)),
	})
	return output, nil
}

func (r *chatRuntime) maybeStartAutoExplore(task string) {
	if !shouldAutoStartExplore(task, r.cfg, r.jobs) {
		return
	}
	subtask := buildAutoExploreTask(task)
	id, err := r.jobs.StartWithRole(backgroundRoleExplore, subtask)
	if err != nil {
		return
	}
	r.injectOrchestratorNote(id)
}

func (r *chatRuntime) injectOrchestratorNote(jobID string) {
	msgs := r.session.Messages()
	msgs = append(msgs, model.Message{
		Role: "system",
		Content: "Internal orchestration note: background exploration job " + jobID +
			" is running for the current user request. Keep making progress in the main conversation. " +
			"Use inspect_background_job or list_background_jobs later if the exploration results would help.",
	})
	r.session.ReplaceMessages(msgs)
	r.dirtySession = true
}

func (r *chatRuntime) tryHandleNaturalLanguageMemory(task string) (bool, string, error) {
	intent, ok := parseNaturalLanguageMemoryIntent(task)
	if !ok {
		return false, "", nil
	}

	output, err := r.applyMemoryIntent(intent)
	if err != nil {
		return true, "", err
	}

	_ = session.AppendTranscript(r.transcriptPath, "user", task)
	_ = session.AppendTranscript(r.transcriptPath, "assistant", output)
	r.appendLocalAssistantExchange(task, output)
	if err := r.save(); err != nil {
		return true, "", err
	}
	return true, output, nil
}

func (r *chatRuntime) maybeApplyReadyJobSummaries() {
	if r.jobs == nil {
		return
	}
	snaps := r.jobs.CollectReadyForApply()
	if len(snaps) == 0 {
		return
	}
	for _, snap := range snaps {
		r.injectJobSummary(snap)
		r.advanceTodoWithJobSummary(snap)
		r.jobs.MarkApplied(snap.ID)
	}
}

func (r *chatRuntime) advanceTodoWithJobSummary(snap jobSnapshot) {
	if r.loop == nil {
		return
	}
	items := append([]tools.TodoItem(nil), r.loop.TodoItems()...)
	if len(items) == 0 {
		next := uniqueTodoTexts(snap.Summary.NextSteps, nil)
		if len(next) == 0 {
			return
		}
		items = make([]tools.TodoItem, 0, len(next))
		for _, text := range next {
			items = append(items, tools.TodoItem{Text: text, Status: "pending"})
		}
		_ = r.loop.ReplaceTodo(items)
		return
	}

	updated := append([]tools.TodoItem(nil), items...)
	for i := range updated {
		if updated[i].Status == "in_progress" {
			updated[i].Status = "completed"
			break
		}
	}
	next := uniqueTodoTexts(snap.Summary.NextSteps, updated)
	for _, text := range next {
		updated = append(updated, tools.TodoItem{Text: text, Status: "pending"})
	}
	if sameTodoItems(items, updated) {
		return
	}
	_ = r.loop.ReplaceTodo(updated)
}

func uniqueTodoTexts(texts []string, existing []tools.TodoItem) []string {
	seen := make(map[string]bool, len(existing)+len(texts))
	for _, item := range existing {
		key := strings.ToLower(strings.TrimSpace(item.Text))
		if key != "" {
			seen[key] = true
		}
	}
	var out []string
	for _, text := range texts {
		text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
		key := strings.ToLower(text)
		if text == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, text)
	}
	return out
}

func sameTodoItems(a, b []tools.TodoItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (r *chatRuntime) applyMemoryIntent(intent memoryIntent) (string, error) {
	switch intent.action {
	case memoryActionShow:
		return memory.FormatNotes(r.globalMemory, r.projectMemory), nil
	case memoryActionRemember:
		note, ok := normalizeRememberedNote(intent.body)
		if !ok {
			return i18n.T("mem.reject"), nil
		}
		scope := intent.scope
		if scope == "" {
			scope = inferMemoryScope(note)
		}
		if scope == memoryScopeGlobal {
			r.globalMemory = memory.Add(r.globalMemory, note)
		} else {
			r.projectMemory = memory.Add(r.projectMemory, note)
			scope = memoryScopeProject
		}
		r.refreshMemoryContext()
		if err := r.saveMemory(); err != nil {
			return "", err
		}
		if scope == memoryScopeGlobal {
			return i18n.T("mem.saved.global"), nil
		}
		return i18n.T("mem.saved.project"), nil
	case memoryActionForget:
		scope := intent.scope
		query := normalizeForgetQuery(intent.body)
		if isLastMemoryQuery(query) {
			desc, err := r.forgetLastMemory(scope)
			if err != nil {
				return "", err
			}
			return desc, nil
		}
		desc, err := r.forgetMatchingMemory(scope, query)
		if err != nil {
			return "", err
		}
		return desc, nil
	default:
		return "", nil
	}
}

func (r *chatRuntime) appendLocalAssistantExchange(userText, assistantText string) {
	msgs := r.session.Messages()
	msgs = append(msgs,
		model.Message{Role: "user", Content: strings.TrimSpace(userText)},
		model.Message{Role: "assistant", Content: strings.TrimSpace(assistantText)},
	)
	r.session.ReplaceMessages(msgs)
	r.dirtySession = true
}

func (r *chatRuntime) forgetLastMemory(scope string) (string, error) {
	switch scope {
	case memoryScopeGlobal:
		if len(r.globalMemory) == 0 {
			return i18n.T("mem.no.global.delete"), nil
		}
		r.globalMemory = append([]string(nil), r.globalMemory[:len(r.globalMemory)-1]...)
	case memoryScopeProject:
		if len(r.projectMemory) == 0 {
			return i18n.T("mem.no.project.delete"), nil
		}
		r.projectMemory = append([]string(nil), r.projectMemory[:len(r.projectMemory)-1]...)
	default:
		switch {
		case len(r.projectMemory) > 0:
			r.projectMemory = append([]string(nil), r.projectMemory[:len(r.projectMemory)-1]...)
			scope = memoryScopeProject
		case len(r.globalMemory) > 0:
			r.globalMemory = append([]string(nil), r.globalMemory[:len(r.globalMemory)-1]...)
			scope = memoryScopeGlobal
		default:
			return i18n.T("mem.no.delete"), nil
		}
	}
	r.refreshMemoryContext()
	if err := r.saveMemory(); err != nil {
		return "", err
	}
	if scope == memoryScopeGlobal {
		return i18n.T("mem.deleted.last.global"), nil
	}
	return i18n.T("mem.deleted.last.project"), nil
}

func (r *chatRuntime) forgetMatchingMemory(scope, query string) (string, error) {
	if query == "" {
		return i18n.T("mem.forget.what"), nil
	}

	removedGlobal := 0
	removedProject := 0
	switch scope {
	case memoryScopeGlobal:
		r.globalMemory, removedGlobal = memory.ForgetMatching(r.globalMemory, query)
	case memoryScopeProject:
		r.projectMemory, removedProject = memory.ForgetMatching(r.projectMemory, query)
	default:
		r.projectMemory, removedProject = memory.ForgetMatching(r.projectMemory, query)
		r.globalMemory, removedGlobal = memory.ForgetMatching(r.globalMemory, query)
	}
	if removedGlobal+removedProject == 0 {
		return i18n.T("mem.no.match"), nil
	}
	r.refreshMemoryContext()
	if err := r.saveMemory(); err != nil {
		return "", err
	}
	if removedGlobal > 0 && removedProject > 0 {
		return fmt.Sprintf(i18n.T("mem.deleted.mixed"), removedGlobal+removedProject), nil
	}
	if removedGlobal > 0 {
		return fmt.Sprintf(i18n.T("mem.deleted.global"), removedGlobal), nil
	}
	return fmt.Sprintf(i18n.T("mem.deleted.project"), removedProject), nil
}

func parseNaturalLanguageMemoryIntent(input string) (memoryIntent, bool) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" || strings.HasPrefix(trimmed, "/") {
		return memoryIntent{}, false
	}

	if body, ok := matchMemoryPrefix(trimmed,
		"remember that ", "remember this: ", "remember: ", "remember ",
		"please remember ", "please remember that ",
		"记住", "记一下", "记着", "请记住", "帮我记住", "请记一下", "帮我记一下",
	); ok {
		return memoryIntent{
			action: memoryActionRemember,
			scope:  detectMemoryScope(trimmed, body),
			body:   body,
		}, true
	}

	if body, ok := matchMemoryPrefix(trimmed,
		"forget this: ", "forget that ", "forget ", "please forget ",
		"忘掉", "忘了", "请忘掉", "帮我忘掉", "请忘了", "帮我忘了",
	); ok {
		return memoryIntent{
			action: memoryActionForget,
			scope:  detectMemoryScope(trimmed, body),
			body:   body,
		}, true
	}

	if isShowMemoryRequest(trimmed) {
		return memoryIntent{action: memoryActionShow, scope: memoryScopeAll}, true
	}
	return memoryIntent{}, false
}

func matchMemoryPrefix(input string, prefixes ...string) (string, bool) {
	trimmed := strings.TrimSpace(input)
	lower := strings.ToLower(trimmed)
	for _, prefix := range prefixes {
		prefixLower := strings.ToLower(prefix)
		if !strings.HasPrefix(lower, prefixLower) || len(trimmed) < len(prefix) {
			continue
		}
		body := strings.TrimSpace(trimmed[len(prefix):])
		body = strings.TrimLeft(body, " :：,，")
		body = strings.TrimSpace(body)
		if body != "" {
			return body, true
		}
	}
	return "", false
}

func isShowMemoryRequest(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	switch lower {
	case "memory", "show memory", "show memories", "show saved memory", "show memory notes",
		"查看记忆", "看看记忆", "显示记忆", "记忆", "有哪些记忆", "看看你记住了什么":
		return true
	default:
		return false
	}
}

func detectMemoryScope(fullInput, body string) string {
	lowerFull := strings.ToLower(fullInput)
	lowerBody := strings.ToLower(body)
	switch {
	case hasAny(lowerFull,
		"global", "globally", "for all projects", "across projects",
		"全局", "通用", "所有项目", "跨项目",
	):
		return memoryScopeGlobal
	case hasAny(lowerFull,
		"this project", "this repo", "this workspace",
		"当前项目", "这个项目", "这个仓库", "当前仓库", "当前工作区",
	):
		return memoryScopeProject
	case isPreferenceMemory(lowerBody):
		return memoryScopeGlobal
	case isProjectMemory(lowerBody):
		return memoryScopeProject
	default:
		return ""
	}
}

func normalizeRememberedNote(text string) (string, bool) {
	text = strings.TrimSpace(text)
	text = strings.Trim(text, "\"'`")
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	if note, ok := normalizeStableMemoryNote(text); ok {
		return note, true
	}
	text = strings.TrimRight(text, ".。!！")
	text = strings.Join(strings.Fields(text), " ")
	if len(text) < 6 || len(text) > 180 {
		return "", false
	}
	return text, true
}

func normalizeForgetQuery(text string) string {
	text = strings.TrimSpace(text)
	text = strings.Trim(text, "\"'`")
	text = strings.TrimLeft(text, "这个这条那条条记忆记录偏好项目全局 ")
	text = strings.TrimSpace(text)
	text = strings.TrimLeft(text, ":：,，")
	return strings.TrimSpace(text)
}

func isLastMemoryQuery(query string) bool {
	lower := strings.ToLower(strings.TrimSpace(query))
	if lower == "" {
		return true
	}
	return hasAny(lower,
		"last", "latest", "most recent",
		"上一条", "最近一条", "刚才那条", "刚刚那条", "刚记住的", "最新那条",
	)
}

func inferMemoryScope(note string) string {
	if isPreferenceMemory(strings.ToLower(note)) {
		return memoryScopeGlobal
	}
	return memoryScopeProject
}

func shouldAutoStartExplore(task string, cfg config.Config, jobs *jobManager) bool {
	task = strings.TrimSpace(task)
	if jobs == nil || cfg.ApprovalMode != tools.ApprovalDangerously {
		return false
	}
	if len(task) < autoExploreMinChars {
		return false
	}
	lower := strings.ToLower(task)
	if !hasAny(lower,
		"analyze", "inspect", "review", "compare", "understand", "explore", "investigate",
		"trace", "audit", "read through", "study",
		"分析", "检查", "阅读", "研究", "梳理", "看看", "对比", "审查", "理解",
	) {
		return false
	}
	if hasAny(lower,
		"small", "tiny", "one file", "single file",
		"小改", "小修改", "单个文件", "一行", "几行",
	) {
		return false
	}
	if strings.Count(lower, " and ") >= 2 || strings.Count(lower, "，") >= 2 || hasAny(lower,
		"repository", "repo", "codebase", "whole project", "whole repo",
		"整个仓库", "整个项目", "代码库", "工作区", "全局",
	) {
		return true
	}
	return hasAny(lower,
		"risks", "architecture", "flow", "dependency", "dependencies", "highest risk",
		"风险", "架构", "流程", "依赖", "调用链", "关键路径",
	)
}

func buildAutoExploreTask(task string) string {
	return strings.TrimSpace(
		"Explore the workspace for this request in read-only mode. " +
			"Do not edit files. Inspect code, commands, and project structure as needed. " +
			"Return a concise summary in exactly these sections:\n" +
			"Key findings:\n" +
			"- ...\n" +
			"Relevant files:\n" +
			"- ...\n" +
			"Risks or unknowns:\n" +
			"- ...\n" +
			"Recommended next steps:\n" +
			"- ...\n\n" +
			"Request: " + task,
	)
}

func (r *chatRuntime) newSession() *agent.Session {
	return r.loop.NewSessionWithPrompt(promptContextFor(r.cfg, r.loop, "chat", memory.RenderSystemMemory(r.globalMemory, r.projectMemory)))
}

func (r *chatRuntime) injectJobSummary(snap jobSnapshot) {
	msgs := r.session.Messages()
	msgs = append(msgs, model.Message{
		Role:    "system",
		Content: "Background result available for the current task. Use it as additional context if relevant:\n" + summarizeJobForSession(snap),
	})
	r.session.ReplaceMessages(msgs)
	r.dirtySession = true
}

func (r *chatRuntime) refreshMemoryContext() {
	if r.jobs != nil {
		r.jobs.UpdateMemory(memory.RenderSystemMemory(r.globalMemory, r.projectMemory))
	}
	messages := r.session.Messages()
	if len(messages) == 0 {
		r.session = r.newSession()
		return
	}
	messages[0].Content = agent.BuildSystemPrompt(promptContextFor(r.cfg, r.loop, "chat", memory.RenderSystemMemory(r.globalMemory, r.projectMemory)))
	r.session.ReplaceMessages(messages)
}

func (r *chatRuntime) save() error {
	return session.Save(r.statePath, session.State{
		SessionName:  r.sessionName,
		Model:        "",
		OutputMode:   "",
		ApprovalMode: "",
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

func (r *chatRuntime) beforeExit(allowAutoMemory bool) {
	if allowAutoMemory && r.autoMemoryExit && r.dirtySession {
		if added, err := r.summarizeMemory(); err != nil {
			fmt.Fprintf(os.Stderr, i18n.T("auto.memory.err"), err)
		} else if added > 0 {
			fmt.Fprintf(os.Stderr, i18n.T("auto.memory.ok"), added)
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

	client := harness.NewFactory(r.cfg).NewModelClient()
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

	client := harness.NewFactory(cfg).NewModelClient()
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

	client := harness.NewFactory(cfg).NewModelClient()
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
	factory := harness.NewFactory(cfg)
	approver := factory.NewApprover(reader, os.Stderr, tools.IsInteractiveTerminal(os.Stdin))
	return factory.NewAgent(approver, os.Stderr, nil), approver
}

func buildAgentWith(cfg config.Config, approver tools.Approver, log io.Writer, jobs tools.JobControl, extraAuditSinks ...tools.ToolAuditSink) *agent.Agent {
	return harness.NewFactory(cfg).NewAgent(approver, log, jobs, extraAuditSinks...)
}

func promptContextFor(cfg config.Config, loop *agent.Agent, sessionMode, memoryText string) agent.PromptContext {
	return harness.BuildPromptContext(cfg, loop, sessionMode, memoryText)
}

func (r *chatRuntime) attachAgentEventSink() {
	if r == nil || r.loop == nil {
		return
	}
	r.loop.SetEventSink(runtimeAgentEventSink{runtime: r})
}

func (r *chatRuntime) recordTrace(ctx context.Context, source, typ string, data map[string]any) {
	if r == nil || r.tracer == nil {
		return
	}
	event := trace.Event{
		Time:    time.Now(),
		Session: r.sessionName,
		Scope:   r.scopeKey,
		Source:  strings.TrimSpace(source),
		Type:    strings.TrimSpace(typ),
		Data:    cloneAnyMap(data),
	}
	_ = r.tracer.Record(ctx, event)
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (r *chatRuntime) traceCommand(fields []string) string {
	if len(fields) == 1 {
		return strings.Join([]string{
			"usage: /trace stats",
			"usage: /trace tail [n]",
		}, "\n")
	}
	switch fields[1] {
	case "stats":
		return r.traceStatusLine()
	case "tail":
		limit := 20
		if len(fields) >= 3 {
			if v, err := strconv.Atoi(fields[2]); err == nil && v > 0 {
				limit = min(v, 200)
			}
		}
		return r.traceTail(limit)
	default:
		return strings.Join([]string{
			"usage: /trace stats",
			"usage: /trace tail [n]",
		}, "\n")
	}
}

func (r *chatRuntime) traceStatusLine() string {
	events, err := trace.ReadTail(r.tracePath, 400)
	if err != nil {
		return "unavailable (" + err.Error() + ")"
	}
	if len(events) == 0 {
		return "path=" + r.tracePath + " events=0"
	}
	counts := trace.CountByType(events)
	type kv struct {
		Key string
		N   int
	}
	items := make([]kv, 0, len(counts))
	for k, n := range counts {
		items = append(items, kv{Key: k, N: n})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].N == items[j].N {
			return items[i].Key < items[j].Key
		}
		return items[i].N > items[j].N
	})
	top := make([]string, 0, min(4, len(items)))
	for _, item := range items[:min(4, len(items))] {
		top = append(top, item.Key+":"+strconv.Itoa(item.N))
	}
	return "path=" + r.tracePath + " events=" + strconv.Itoa(len(events)) + " top=" + strings.Join(top, ",")
}

func (r *chatRuntime) traceTail(limit int) string {
	events, err := trace.ReadTail(r.tracePath, limit)
	if err != nil {
		return "trace read error: " + err.Error()
	}
	if len(events) == 0 {
		return "trace is empty"
	}
	lines := make([]string, 0, len(events))
	for _, event := range events {
		line := event.Time.Format("15:04:05") + " " + event.Source + " " + event.Type
		if event.Data != nil {
			if text := compactJobText(formatCompactTraceData(event.Data), 180); strings.TrimSpace(text) != "" {
				line += " " + text
			}
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func formatCompactTraceData(data map[string]any) string {
	if len(data) == 0 {
		return ""
	}
	type kv struct {
		Key string
		Val string
	}
	items := make([]kv, 0, len(data))
	for key, value := range data {
		text := strings.TrimSpace(fmt.Sprintf("%v", value))
		text = strings.ReplaceAll(text, "\n", " ")
		text = strings.Join(strings.Fields(text), " ")
		if len(text) > 64 {
			text = text[:64] + "..."
		}
		if text == "" {
			continue
		}
		items = append(items, kv{Key: key, Val: text})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, item.Key+"="+item.Val)
	}
	return strings.Join(parts, " ")
}

func (r *chatRuntime) auditCommand(fields []string) string {
	if len(fields) == 1 {
		return strings.Join([]string{
			i18n.T("cmd.audit.usage.stats"),
			i18n.T("cmd.audit.usage.tail"),
			i18n.T("cmd.audit.usage.errors"),
		}, "\n")
	}
	switch fields[1] {
	case "stats":
		return r.auditStatusLine()
	case "tail":
		limit := 10
		if len(fields) >= 3 {
			if v, err := strconv.Atoi(fields[2]); err == nil && v > 0 {
				limit = min(v, 100)
			}
		}
		return r.auditTail(limit)
	case "errors":
		limit := 20
		if len(fields) >= 3 {
			if v, err := strconv.Atoi(fields[2]); err == nil && v > 0 {
				limit = min(v, 200)
			}
		}
		return r.auditErrors(limit)
	default:
		return strings.Join([]string{
			i18n.T("cmd.audit.usage.stats"),
			i18n.T("cmd.audit.usage.tail"),
			i18n.T("cmd.audit.usage.errors"),
		}, "\n")
	}
}

func (r *chatRuntime) auditStatusLine() string {
	events, err := tools.ReadAuditTail(r.auditPath, 200)
	if err != nil {
		return "unavailable (" + err.Error() + ")"
	}
	stats := tools.ComputeAuditStats(events)
	return tools.FormatAuditStats(stats, 3)
}

func (r *chatRuntime) auditTail(limit int) string {
	events, err := tools.ReadAuditTail(r.auditPath, limit)
	if err != nil {
		return "audit read error: " + err.Error()
	}
	if len(events) == 0 {
		return i18n.T("cmd.audit.empty")
	}
	lines := make([]string, 0, len(events))
	for _, event := range events {
		line := event.Time.Format("15:04:05") + " " + event.Tool + " " + event.Status
		if strings.TrimSpace(event.Error) != "" {
			line += " err=" + compactJobText(event.Error, 120)
		}
		if strings.TrimSpace(event.ArgsPreview) != "" {
			line += " args=" + compactJobText(event.ArgsPreview, 120)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (r *chatRuntime) auditErrors(limit int) string {
	events, err := tools.ReadAuditTail(r.auditPath, max(20, limit*4))
	if err != nil {
		return "audit read error: " + err.Error()
	}
	if len(events) == 0 {
		return i18n.T("cmd.audit.empty")
	}
	lines := make([]string, 0, limit)
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if strings.TrimSpace(event.Status) == "" || event.Status == "ok" {
			continue
		}
		line := event.Time.Format("15:04:05") + " " + event.Tool + " " + event.Status
		if strings.TrimSpace(event.Error) != "" {
			line += " err=" + compactJobText(event.Error, 140)
		}
		lines = append(lines, line)
		if len(lines) >= limit {
			break
		}
	}
	if len(lines) == 0 {
		return i18n.T("cmd.audit.no_errors")
	}
	return strings.Join(lines, "\n")
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
	text = model.StripThinkingTags(text)
	switch mode {
	case "terminal":
		return agent.FormatTerminalOutput(text)
	default:
		return strings.TrimSpace(text)
	}
}

func initLanguage(stateDir, mode string) {
	if i18n.LoadFromFile(stateDir) {
		return
	}

	if !shouldPromptForLanguage(mode, tools.IsInteractiveTerminal(os.Stdin)) {
		i18n.Set(defaultStartupLanguage())
		_ = i18n.SaveToFile(stateDir, i18n.Lang())
		return
	}

	fmt.Fprintln(os.Stderr, i18n.T("lang.prompt"))
	fmt.Fprintln(os.Stderr, i18n.T("lang.en"))
	fmt.Fprintln(os.Stderr, i18n.T("lang.cn"))
	fmt.Fprint(os.Stderr, i18n.T("lang.ask"))

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	choice := strings.TrimSpace(line)
	switch choice {
	case "2", "cn", "中文":
		i18n.Set(i18n.LangCN)
	default:
		i18n.Set(i18n.LangEN)
	}
	_ = i18n.SaveToFile(stateDir, i18n.Lang())
	fmt.Fprintln(os.Stderr, i18n.T("lang.saved"))
}

func shouldPromptForLanguage(mode string, interactive bool) bool {
	return mode == "chat" && interactive
}

func defaultStartupLanguage() string {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
		if strings.Contains(value, "zh") || strings.Contains(value, "cn") {
			return i18n.LangCN
		}
		if value != "" {
			return i18n.LangEN
		}
	}
	return i18n.LangEN
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  tacli                 # default chat on interactive terminals")
	fmt.Fprintln(os.Stderr, "  tacli -d              # default chat in dangerously mode")
	fmt.Fprintln(os.Stderr, "  tacli chat")
	fmt.Fprintln(os.Stderr, "  tacli run [--dangerously] <task>")
	fmt.Fprintln(os.Stderr, "  tacli <task>          # shorthand for run")
	fmt.Fprintln(os.Stderr, "  tacli ping [flags]")
	fmt.Fprintln(os.Stderr, "  tacli models [flags]")
	fmt.Fprintln(os.Stderr, "  tacli version")
	fmt.Fprintln(os.Stderr)
	printRunUsage()
}

func printRunUsage() {
	fmt.Fprintln(os.Stderr, "Examples:")
	fmt.Fprintln(os.Stderr, `  tacli`)
	fmt.Fprintln(os.Stderr, `  tacli -d`)
	fmt.Fprintln(os.Stderr, `  tacli "inspect this repo"`)
	fmt.Fprintln(os.Stderr, `  tacli -d "run go test ./..."`)
	fmt.Fprintln(os.Stderr, `  tacli run --dangerously "run go test ./..."`)
	fmt.Fprintln(os.Stderr, `  tacli chat`)
	fmt.Fprintln(os.Stderr, `  tacli chat --session bugfix`)
}
