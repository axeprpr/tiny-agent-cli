package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tiny-agent-cli/internal/agent"
	"tiny-agent-cli/internal/config"
	"tiny-agent-cli/internal/harness"
	"tiny-agent-cli/internal/i18n"
	"tiny-agent-cli/internal/mcp"
	"tiny-agent-cli/internal/memory"
	"tiny-agent-cli/internal/model"
	"tiny-agent-cli/internal/model/openaiapi"
	"tiny-agent-cli/internal/plugins"
	"tiny-agent-cli/internal/session"
	"tiny-agent-cli/internal/tasks"
	"tiny-agent-cli/internal/tools"
	"tiny-agent-cli/internal/trace"
	"tiny-agent-cli/internal/transport"
)

var version = "dev"

type runtimeOptions struct {
	outputMode     string
	conversation   string
	autoMemoryExit bool
}

type chatRuntime struct {
	cfg                config.Config
	reader             *bufio.Reader
	approver           tools.Approver
	loop               *agent.Agent
	session            *agent.Session
	jobs               *jobManager
	sessionName        string
	sessionID          string
	parentSession      string
	outputMode         string
	transcriptPath     string
	statePath          string
	memoryPath         string
	auditPath          string
	tracePath          string
	permissionPath     string
	teamKey            string
	scopeKey           string
	globalMemory       []string
	teamMemory         []string
	projectMemory      []string
	permissions        *tools.PermissionStore
	taskStore          *tasks.Store
	modelContextWindow int
	autoMemoryExit     bool
	dirtySession       bool
	tracer             *trace.FileSink
	pluginManager      *plugins.Manager
	pluginCommands     map[string]plugins.Command
	skills             []tools.Skill
	foregroundMu       sync.Mutex
	foregroundCancel   context.CancelFunc
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
	case "init":
		return runInit(args[1:])
	case "plan":
		return runPlan(args[1:])
	case "run":
		return runTask(withDangerouslyFlag(args[1:], globalDangerously))
	case "chat":
		return runChat(withDangerouslyFlag(args[1:], globalDangerously))
	case "status":
		return runStatus(args[1:])
	case "contract":
		return runContract(args[1:])
	case "skills":
		return runSkills(args[1:])
	case "capabilities", "capability":
		return runCapabilities(args[1:])
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
		if isRunShorthand(args) {
			return runTask(withDangerouslyFlag(args, globalDangerously))
		}
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		printUsage()
		return 2
	}
}

func isRunShorthand(args []string) bool {
	if len(args) != 1 {
		return false
	}
	return strings.ContainsAny(args[0], " \t\r\n")
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

	factory := harness.NewFactory(cfg)
	approver := factory.NewApprover(reader, os.Stderr, tools.IsInteractiveTerminal(os.Stdin))
	logWriter := io.Writer(os.Stderr)
	if transport.IsStructuredMode(opts.outputMode) {
		logWriter = io.Discard
	}
	loop := factory.NewAgent(approver, logWriter, nil, nil)

	switch transport.NormalizeOutputMode(opts.outputMode) {
	case transport.OutputJSON:
		result, err := loop.Run(context.Background(), task)
		payloadMap := map[string]any{
			"type":  "result",
			"final": formatRunOutput(result.Final, transport.OutputRaw),
			"steps": result.Steps,
		}
		if err != nil {
			payloadMap["type"] = "error"
			payloadMap["error"] = err.Error()
		}
		payload, marshalErr := json.Marshal(payloadMap)
		if marshalErr != nil {
			fmt.Fprintf(os.Stderr, "json output error: %v\n", marshalErr)
			return 1
		}
		fmt.Println(string(payload))
		if err != nil {
			return 1
		}
		return 0
	case transport.OutputJSONL, transport.OutputStructured:
		writer := transport.NewStructuredWriter(os.Stdout)
		loop.SetEventSink(transport.AgentEventSink{Writer: writer})
		_ = writer.Emit("run_start", map[string]any{
			"task":     task,
			"model":    cfg.Model,
			"workdir":  cfg.WorkDir,
			"approval": cfg.ApprovalMode,
		})
		result, err := loop.NewSession().RunTaskStreaming(context.Background(), task, func(token string) {
			_ = writer.EmitToken(token)
		})
		if err != nil {
			_ = writer.EmitError(err)
			return 1
		}
		_ = writer.EmitResult(formatRunOutput(result.Final, transport.OutputRaw), result.Steps)
		return 0
	default:
		result, err := loop.Run(context.Background(), task)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
			return 1
		}
		fmt.Println(formatRunOutput(result.Final, opts.outputMode))
		return 0
	}
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

	tasks, err := readNonInteractiveTasks(reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "chat input error: %v\n", err)
		runtime.beforeExit(true)
		return 1
	}
	for _, task := range tasks {
		if strings.HasPrefix(task, "/") {
			result := runtime.executeCommand(task)
			if result.handled {
				if strings.TrimSpace(result.output) != "" {
					fmt.Fprintln(os.Stdout, result.output)
				}
				if result.exitCode >= 0 {
					runtime.beforeExit(true)
					return result.exitCode
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
	}
	runtime.beforeExit(true)
	return 0
}

func readNonInteractiveTasks(reader *bufio.Reader) ([]string, error) {
	if reader == nil {
		return nil, nil
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil, nil
	}
	if !strings.ContainsRune(text, '\x00') {
		return []string{text}, nil
	}
	parts := strings.Split(text, "\x00")
	tasks := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tasks = append(tasks, part)
	}
	return tasks, nil
}

func newChatRuntime(cfg config.Config, opts runtimeOptions, reader *bufio.Reader) (*chatRuntime, error) {
	loop, approver := buildAgent(cfg, reader)
	sessionName := resolveChatSessionName(opts.conversation, time.Now())

	r := &chatRuntime{
		cfg:            cfg,
		reader:         reader,
		approver:       approver,
		loop:           loop,
		outputMode:     opts.outputMode,
		autoMemoryExit: opts.autoMemoryExit,
		memoryPath:     memory.Path(cfg.StateDir),
		auditPath:      tools.AuditPath(cfg.StateDir),
		teamKey:        resolveTeamKey(cfg),
		scopeKey:       memory.ScopeKey(cfg.WorkDir),
		taskStore:      tasks.New(filepath.Join(cfg.WorkDir, ".tacli", "tasks.json")),
		pluginCommands: make(map[string]plugins.Command),
	}
	r.permissionPath = tools.PermissionPath(cfg.StateDir)
	r.permissions = loadRuntimePolicy(cfg)
	if err := r.pullRemoteSettings(); err != nil {
		r.recordTrace(context.Background(), "runtime", "settings_pull_error", map[string]any{"error": err.Error()})
	}
	r.teamKey = resolveTeamKey(r.cfg)
	r.setSessionName(sessionName)
	r.attachAgentEventSink()
	r.rebuildLoop()
	r.clearPlanningState()
	r.refreshModelMetadata()
	if mem, err := memory.Load(r.memoryPath); err == nil {
		r.globalMemory = mem.Global
		r.teamMemory = mem.Teams[r.teamKey]
		r.projectMemory = mem.Projects[r.scopeKey]
	}
	r.session = r.newSession()
	r.setSessionIdentity(session.NewSessionID(), "")
	r.jobs = newJobManager(cfg, r.renderSystemMemory())
	r.jobs.SetRoleRouter(llmBackgroundRoleRouter(cfg))
	if discovered, err := tools.DiscoverSkills(cfg.WorkDir); err == nil {
		r.skills = discovered
	}
	if manager, err := plugins.NewManager(); err == nil {
		r.pluginManager = manager
		_, _ = manager.Discover()
	}

	if strings.TrimSpace(opts.conversation) != "" {
		if _, err := r.loadCurrentSessionState(); err != nil {
			return nil, err
		}
	}
	r.recordTrace(context.Background(), "runtime", "runtime_started", map[string]any{
		"model":        cfg.Model,
		"workdir":      cfg.WorkDir,
		"conversation": r.sessionName,
		"trace_path":   r.tracePath,
	})

	return r, nil
}

type runtimeCommandResult struct {
	handled           bool
	output            string
	exitCode          int
	reloadSessionView bool
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
		r.clearPlanningState()
		r.session = r.newSession()
		_ = r.save()
		return runtimeCommandResult{handled: true, output: i18n.T("cmd.reset"), exitCode: -1, reloadSessionView: true}
	case "/help":
		return runtimeCommandResult{handled: true, output: fmt.Sprintf("tacli %s\n\n%s", version, i18n.T("help")), exitCode: -1}
	case "/interrupt":
		if r.interruptForegroundTask() {
			return runtimeCommandResult{handled: true, output: i18n.T("cmd.interrupt.ok"), exitCode: -1}
		}
		return runtimeCommandResult{handled: true, output: i18n.T("cmd.interrupt.idle"), exitCode: -1}
	case "/steer":
		body := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), fields[0]))
		if body == "" {
			return runtimeCommandResult{handled: true, output: "usage: /steer <message>", exitCode: -1}
		}
		err := r.enqueueSteeringMessage(body)
		if err != nil {
			return runtimeCommandResult{handled: true, output: err.Error(), exitCode: -1}
		}
		return runtimeCommandResult{handled: true, output: "queued steering message", exitCode: -1}
	case "/follow", "/followup":
		body := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), fields[0]))
		if body == "" {
			return runtimeCommandResult{handled: true, output: "usage: /follow <message>", exitCode: -1}
		}
		err := r.enqueueFollowUpMessage(body)
		if err != nil {
			return runtimeCommandResult{handled: true, output: err.Error(), exitCode: -1}
		}
		return runtimeCommandResult{handled: true, output: "queued follow-up message", exitCode: -1}
	case "/init":
		report, err := initializeRepo(r.cfg.WorkDir)
		if err != nil {
			return runtimeCommandResult{handled: true, output: "init error: " + err.Error(), exitCode: -1}
		}
		return runtimeCommandResult{handled: true, output: report.render(), exitCode: -1}
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
		settingsSummary := "settings_sync=disabled"
		if strings.TrimSpace(r.cfg.SettingsURL) != "" {
			settingsSummary = fmt.Sprintf("settings_sync=%t endpoint=%s", r.cfg.SettingsSync, r.cfg.SettingsURL)
		}
		commandRuleSummary := "command_rules=0"
		if r.permissions != nil {
			commandRuleSummary = fmt.Sprintf("command_rules=%d", len(r.permissions.CommandRules()))
		}
		return runtimeCommandResult{handled: true, output: fmt.Sprintf("conversation=%s\nsession_id=%s\nparent_session=%s\nteam=%s\nmemory_scope=%s\nglobal_memory_notes=%d\nteam_memory_notes=%d\nproject_memory_notes=%d\nstate=%s\ntranscript=%s\nmemory=%s\npolicy=%s\naudit=%s\ntrace=%s\n%s\n%s\n%s\n%s",
			r.sessionName, firstNonEmpty(r.sessionID, "(none)"), firstNonEmpty(r.parentSession, "(none)"), firstNonEmpty(r.teamKey, "(none)"), r.scopeKey, len(r.globalMemory), len(r.teamMemory), len(r.projectMemory), r.statePath, r.transcriptPath, r.memoryPath, r.permissionPath, auditSummary, traceSummary, settingsSummary, jobSummary, pluginSummary, commandRuleSummary), exitCode: -1}
	case "/contract":
		return runtimeCommandResult{handled: true, output: r.contractCommand(), exitCode: -1}
	case "/save":
		return runtimeCommandResult{handled: true, output: r.saveCommand(), exitCode: -1}
	case "/plan":
		return runtimeCommandResult{handled: true, output: r.planCommand(), exitCode: -1}
	case "/compact":
		if r.session.Compact() {
			r.dirtySession = true
			_ = r.save()
			return runtimeCommandResult{handled: true, output: "conversation compacted", exitCode: -1}
		}
		return runtimeCommandResult{handled: true, output: "conversation did not need compaction", exitCode: -1}
	case "/new":
		target := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), fields[0]))
		started, err := r.startNewConversation(target)
		if err != nil {
			return runtimeCommandResult{handled: true, output: fmt.Sprintf("conversation create error: %v", err), exitCode: -1}
		}
		return runtimeCommandResult{handled: true, output: started, exitCode: -1, reloadSessionView: true}
	case "/resume":
		if len(fields) == 1 {
			return runtimeCommandResult{handled: true, output: r.describeConversations(), exitCode: -1}
		}
		target := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), fields[0]))
		loaded, err := r.resumeConversation(target)
		if err != nil {
			return runtimeCommandResult{handled: true, output: fmt.Sprintf("conversation resume error: %v", err), exitCode: -1}
		}
		if !loaded {
			return runtimeCommandResult{handled: true, output: fmt.Sprintf("no saved conversation %s", target), exitCode: -1}
		}
		return runtimeCommandResult{handled: true, output: fmt.Sprintf("conversation resumed: %s", r.sessionName), exitCode: -1, reloadSessionView: true}
	case "/tree":
		return runtimeCommandResult{handled: true, output: r.describeConversationTree(), exitCode: -1}
	case "/rename":
		if len(fields) < 2 {
			return runtimeCommandResult{handled: true, output: "usage: /rename <name>", exitCode: -1}
		}
		target := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), fields[0]))
		output, err := r.renameConversation(target)
		if err != nil {
			return runtimeCommandResult{handled: true, output: fmt.Sprintf("conversation rename error: %v", err), exitCode: -1}
		}
		return runtimeCommandResult{handled: true, output: output, exitCode: -1, reloadSessionView: true}
	case "/fork":
		target := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), fields[0]))
		output, err := r.forkConversation(target)
		if err != nil {
			return runtimeCommandResult{handled: true, output: fmt.Sprintf("conversation fork error: %v", err), exitCode: -1}
		}
		return runtimeCommandResult{handled: true, output: output, exitCode: -1, reloadSessionView: true}
	case "/agents":
		return runtimeCommandResult{handled: true, output: r.agentsCommand(fields), exitCode: -1}
	case "/hooks":
		return runtimeCommandResult{handled: true, output: r.hooksCommand(fields), exitCode: -1}
	case "/mcp":
		return runtimeCommandResult{handled: true, output: r.mcpCommand(fields, input), exitCode: -1}
	case "/plugin":
		return runtimeCommandResult{handled: true, output: r.pluginCommand(fields, input), exitCode: -1}
	case "/reload-plugins":
		return runtimeCommandResult{handled: true, output: r.reloadPluginsCommand(), exitCode: -1}
	case "/skills":
		return runtimeCommandResult{handled: true, output: r.skillsCommand(), exitCode: -1}
	case "/capabilities", "/capability":
		return runtimeCommandResult{handled: true, output: r.capabilitiesCommand(fields), exitCode: -1}
	case "/audit":
		return runtimeCommandResult{handled: true, output: r.auditCommand(fields), exitCode: -1}
	case "/debug-tool-call":
		return runtimeCommandResult{handled: true, output: r.debugToolCallCommand(fields), exitCode: -1}
	case "/debug-turn":
		return runtimeCommandResult{handled: true, output: r.debugTurnCommand(fields), exitCode: -1}
	case "/debug-runtime":
		return runtimeCommandResult{handled: true, output: r.debugRuntimeCommand(), exitCode: -1}
	case "/trace":
		return runtimeCommandResult{handled: true, output: r.traceCommand(fields), exitCode: -1}
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
		_ = r.pushRemoteSettings()
		return runtimeCommandResult{handled: true, output: fmt.Sprintf(i18n.T("cmd.approval.set"), r.approver.Mode()), exitCode: -1}
	case "/output":
		return runtimeCommandResult{handled: true, output: i18n.T("cmd.output.deprecated"), exitCode: -1}
	case "/model":
		modelName, verify, err := parseModelCommand(fields)
		if err != nil {
			return runtimeCommandResult{handled: true, output: err.Error(), exitCode: -1}
		}
		verificationNotice := ""
		if verify {
			ok, verifyErr := r.verifyModelAvailable(modelName)
			if verifyErr != nil {
				verificationNotice = "\nmodel verification skipped: " + verifyErr.Error()
			} else if !ok {
				return runtimeCommandResult{
					handled:  true,
					output:   fmt.Sprintf("model %q was not found on the current endpoint; use /model --no-verify %s to force", modelName, modelName),
					exitCode: -1,
				}
			}
		}
		r.cfg.Model = modelName
		r.rebuildLoop()
		r.refreshModelMetadata()
		_ = r.pushRemoteSettings()
		return runtimeCommandResult{handled: true, output: fmt.Sprintf(i18n.T("cmd.model.set"), r.cfg.Model) + verificationNotice, exitCode: -1}
	case "/policy":
		return runtimeCommandResult{handled: true, output: r.policyCommand(fields, input), exitCode: -1}
	case "/bg":
		id, err := r.jobs.Start(strings.TrimSpace(input[len("/bg"):]))
		if err != nil {
			return runtimeCommandResult{handled: true, output: err.Error(), exitCode: -1}
		}
		role := backgroundRoleGeneral
		isolation := "shared"
		if snap, ok := r.jobs.Snapshot(id); ok {
			role = normalizeBackgroundRole(snap.Role)
			isolation = normalizeBackgroundIsolation(snap.Role, snap.Isolation)
		}
		return runtimeCommandResult{handled: true, output: fmt.Sprintf("%s (role=%s isolation=%s)", fmt.Sprintf(i18n.T("cmd.bg.started"), id), role, isolation), exitCode: -1}
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
		isolation := "shared"
		if snap, ok := r.jobs.Snapshot(id); ok {
			normalized = normalizeBackgroundRole(snap.Role)
			isolation = normalizeBackgroundIsolation(snap.Role, snap.Isolation)
		}
		return runtimeCommandResult{handled: true, output: fmt.Sprintf("%s (role=%s isolation=%s)", fmt.Sprintf(i18n.T("cmd.bg.started"), id), normalized, isolation), exitCode: -1}
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
	case "/tasks":
		return runtimeCommandResult{handled: true, output: r.tasksCommand(fields, input), exitCode: -1}
	case "/review":
		return runtimeCommandResult{handled: true, output: r.reviewCommand(fields), exitCode: -1}
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

func (r *chatRuntime) setForegroundCancel(cancel context.CancelFunc) {
	if r == nil {
		return
	}
	r.foregroundMu.Lock()
	defer r.foregroundMu.Unlock()
	r.foregroundCancel = cancel
}

func (r *chatRuntime) clearForegroundCancel(_ context.CancelFunc) {
	if r == nil {
		return
	}
	r.foregroundMu.Lock()
	defer r.foregroundMu.Unlock()
	r.foregroundCancel = nil
}

func (r *chatRuntime) interruptForegroundTask() bool {
	if r == nil {
		return false
	}
	r.foregroundMu.Lock()
	cancel := r.foregroundCancel
	r.foregroundMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (r *chatRuntime) enqueueSteeringMessage(text string) error {
	if r == nil || r.session == nil {
		return fmt.Errorf("session is not configured")
	}
	if ok := r.session.QueueSteeringMessage(text); !ok {
		return fmt.Errorf("message is empty")
	}
	return nil
}

func (r *chatRuntime) enqueueFollowUpMessage(text string) error {
	if r == nil || r.session == nil {
		return fmt.Errorf("session is not configured")
	}
	if ok := r.session.QueueFollowUpMessage(text); !ok {
		return fmt.Errorf("message is empty")
	}
	return nil
}

func parseModelCommand(fields []string) (string, bool, error) {
	if len(fields) < 2 {
		return "", true, errors.New(i18n.T("cmd.model.usage"))
	}
	verify := true
	args := append([]string(nil), fields[1:]...)
	for len(args) > 0 {
		switch args[0] {
		case "--no-verify":
			verify = false
			args = args[1:]
		case "--verify":
			verify = true
			args = args[1:]
		default:
			goto done
		}
	}
done:
	if len(args) == 0 {
		return "", verify, errors.New(i18n.T("cmd.model.usage"))
	}
	return strings.TrimSpace(strings.Join(args, " ")), verify, nil
}

func (r *chatRuntime) verifyModelAvailable(name string) (bool, error) {
	if r == nil {
		return false, fmt.Errorf("runtime is not configured")
	}
	cfg := r.cfg
	cfg.Model = strings.TrimSpace(name)
	client := harness.NewFactory(cfg).NewModelClient()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, ok, err := client.ModelInfo(ctx, cfg.Model)
	if err != nil {
		return false, err
	}
	return ok, nil
}

func (r *chatRuntime) rebuildLoop() {
	var (
		todoItems    []tools.TodoItem
		taskContract tools.TaskContract
	)
	if r.session != nil && r.loop != nil {
		todoItems = append([]tools.TodoItem(nil), r.loop.TodoItems()...)
		taskContract = r.loop.TaskContract()
	}
	var jobs tools.JobControl
	if r.jobs != nil {
		jobs = jobToolAdapter{manager: r.jobs}
	}
	r.loop = newRuntimeKernel(r.cfg, r.approver, os.Stderr, jobs, r.permissions).buildAgent()
	r.attachAgentEventSink()
	r.applyLoadedPlugins()
	if r.session != nil {
		r.session.SetAgent(r.loop)
	}
	r.applyPlanningState(todoItems, taskContract)
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

func (r *chatRuntime) setSessionIdentity(id, parent string) {
	r.sessionID = strings.TrimSpace(id)
	r.parentSession = strings.TrimSpace(parent)
	if r.sessionID == "" {
		r.sessionID = session.NewSessionID()
	}
}

func (r *chatRuntime) loadCurrentSessionState() (bool, error) {
	return r.resumeConversation(r.sessionName)
}

func (r *chatRuntime) resumeConversation(name string) (bool, error) {
	next := strings.TrimSpace(name)
	if next == "" {
		next = r.sessionName
	}
	path := session.SessionPath(r.cfg.StateDir, next)
	state, err := session.Load(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	r.setSessionName(next)
	r.setSessionIdentity(state.SessionID, state.ParentSession)
	if len(state.Messages) > 0 {
		r.session.ReplaceMessages(state.Messages)
	}
	if strings.TrimSpace(state.Model) != "" {
		r.cfg.Model = state.Model
	}
	if strings.TrimSpace(state.OutputMode) != "" {
		r.outputMode = state.OutputMode
	}
	if strings.TrimSpace(state.ApprovalMode) != "" {
		r.cfg.ApprovalMode = state.ApprovalMode
		if r.approver != nil {
			_ = r.approver.SetMode(state.ApprovalMode)
		}
	}
	r.clearPlanningState()
	r.rebuildLoop()
	r.applyPlanningState(state.TodoItems, state.TaskContract)
	r.refreshModelMetadata()
	if state.TeamKey == r.teamKey && state.ScopeKey == r.scopeKey {
		r.globalMemory = append([]string(nil), state.GlobalMemory...)
		r.teamMemory = append([]string(nil), state.TeamMemory...)
		r.projectMemory = append([]string(nil), state.ProjectMemory...)
		r.refreshMemoryContext()
	}
	return true, nil
}

func (r *chatRuntime) resumeOrCreateConversation(name string) (string, error) {
	next := resolveChatSessionName(name, time.Now())
	if next == r.sessionName {
		return fmt.Sprintf("already on conversation %s", r.sessionName), nil
	}
	if err := r.save(); err != nil {
		return "", err
	}

	r.setSessionName(next)
	r.clearPlanningState()
	r.session = r.newSession()
	r.setSessionIdentity(session.NewSessionID(), "")
	r.dirtySession = false

	loaded, err := r.loadCurrentSessionState()
	if err != nil {
		return "", err
	}
	if err := r.save(); err != nil {
		return "", err
	}
	if loaded {
		r.recordTrace(context.Background(), "runtime", "session_switched", map[string]any{"session": r.sessionName, "loaded": true})
		return fmt.Sprintf("conversation resumed: %s", r.sessionName), nil
	}
	r.recordTrace(context.Background(), "runtime", "session_switched", map[string]any{"session": r.sessionName, "loaded": false})
	return fmt.Sprintf("started conversation %s", r.sessionName), nil
}

func (r *chatRuntime) describeConversations() string {
	lines := []string{
		"current_conversation=" + r.sessionName,
		"usage: /resume",
		"usage: /resume <name>",
		"usage: /new [name]",
		"usage: /rename <name>",
		"usage: /fork [name]",
		"usage: /tree",
	}

	summaries, err := session.ListSessions(r.cfg.StateDir)
	if err != nil || len(summaries) == 0 {
		return strings.Join(lines, "\n")
	}

	if len(summaries) > 8 {
		summaries = summaries[:8]
	}
	lines = append(lines, "recent conversations:")
	for _, item := range summaries {
		line := fmt.Sprintf("- %s", item.Name)
		if strings.TrimSpace(item.SessionID) != "" {
			line += " id=" + item.SessionID
		}
		if strings.TrimSpace(item.Parent) != "" {
			line += " parent=" + item.Parent
		}
		if !item.SavedAt.IsZero() {
			line += " saved=" + item.SavedAt.Format(time.RFC3339)
		}
		if item.MessageCount > 0 {
			line += fmt.Sprintf(" messages=%d", item.MessageCount)
		}
		if strings.TrimSpace(item.Model) != "" {
			line += " model=" + item.Model
		}
		if strings.TrimSpace(item.ApprovalMode) != "" {
			line += " approval=" + item.ApprovalMode
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (r *chatRuntime) describeConversationTree() string {
	lines := []string{
		"current_conversation=" + r.sessionName,
		"usage: /tree",
	}
	roots, err := session.BuildSessionTree(r.cfg.StateDir)
	if err != nil {
		lines = append(lines, "tree_error="+err.Error())
		return strings.Join(lines, "\n")
	}
	if len(roots) == 0 {
		lines = append(lines, "conversation tree: (empty)")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "conversation tree:")
	for i, root := range roots {
		appendConversationTreeLines(&lines, root, "", i == len(roots)-1, r.sessionName)
	}
	return strings.Join(lines, "\n")
}

func appendConversationTreeLines(lines *[]string, node *session.TreeNode, prefix string, last bool, current string) {
	if node == nil || lines == nil {
		return
	}
	connector := "├─ "
	nextPrefix := prefix + "│  "
	if last {
		connector = "└─ "
		nextPrefix = prefix + "   "
	}
	summary := node.Summary
	label := summary.Name
	if strings.TrimSpace(summary.SessionID) != "" {
		label += " [" + summary.SessionID + "]"
	}
	if summary.Name == current {
		label += " (current)"
	}
	if !summary.SavedAt.IsZero() {
		label += " saved=" + summary.SavedAt.Format(time.RFC3339)
	}
	*lines = append(*lines, prefix+connector+label)
	for i, child := range node.Children {
		appendConversationTreeLines(lines, child, nextPrefix, i == len(node.Children)-1, current)
	}
}

func (r *chatRuntime) startNewConversation(name string) (string, error) {
	next := resolveChatSessionName(name, time.Now())
	if next == r.sessionName {
		return fmt.Sprintf("already on conversation %s", r.sessionName), nil
	}
	if _, err := os.Stat(session.SessionPath(r.cfg.StateDir, next)); err == nil {
		return "", fmt.Errorf("conversation %s already exists; use /resume %s", next, next)
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := r.save(); err != nil {
		return "", err
	}
	r.setSessionName(next)
	r.clearPlanningState()
	r.session = r.newSession()
	r.setSessionIdentity(session.NewSessionID(), "")
	r.dirtySession = false
	if err := r.save(); err != nil {
		return "", err
	}
	return fmt.Sprintf("started conversation %s", r.sessionName), nil
}

func (r *chatRuntime) renameConversation(name string) (string, error) {
	next := strings.TrimSpace(name)
	if next == "" {
		return "", fmt.Errorf("missing conversation name")
	}
	if next == r.sessionName {
		return fmt.Sprintf("conversation already named %s", r.sessionName), nil
	}
	targetState := session.SessionPath(r.cfg.StateDir, next)
	if _, err := os.Stat(targetState); err == nil {
		return "", fmt.Errorf("conversation %s already exists", next)
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	oldName := r.sessionName
	oldState, oldTranscript, oldTrace := r.statePath, r.transcriptPath, r.tracePath
	if err := r.save(); err != nil {
		return "", err
	}
	if err := renameFileIfExists(oldState, targetState); err != nil {
		return "", err
	}
	if err := renameFileIfExists(oldTranscript, session.TranscriptPath(r.cfg.StateDir, next)); err != nil {
		return "", err
	}
	if err := renameFileIfExists(oldTrace, trace.Path(r.cfg.StateDir, next)); err != nil {
		return "", err
	}
	r.setSessionName(next)
	r.setSessionIdentity(r.sessionID, r.parentSession)
	if err := r.save(); err != nil {
		return "", err
	}
	return fmt.Sprintf("renamed conversation %s -> %s", oldName, next), nil
}

func (r *chatRuntime) forkConversation(name string) (string, error) {
	next := strings.TrimSpace(name)
	if next == "" {
		next = fmt.Sprintf("%s-fork-%s", r.sessionName, time.Now().UTC().Format("20060102-150405"))
	}
	if next == r.sessionName {
		return "", fmt.Errorf("fork name must differ from current conversation")
	}
	targetState := session.SessionPath(r.cfg.StateDir, next)
	if _, err := os.Stat(targetState); err == nil {
		return "", fmt.Errorf("conversation %s already exists", next)
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	parentRef := r.sessionID
	if parentRef == "" {
		parentRef = r.statePath
	}
	state := session.State{
		SessionID:     session.NewSessionID(),
		ParentSession: parentRef,
		SessionName:   next,
		Model:         r.cfg.Model,
		OutputMode:    r.outputMode,
		ApprovalMode:  r.cfg.ApprovalMode,
		TeamKey:       r.teamKey,
		ScopeKey:      r.scopeKey,
		GlobalMemory:  append([]string(nil), r.globalMemory...),
		TeamMemory:    append([]string(nil), r.teamMemory...),
		ProjectMemory: append([]string(nil), r.projectMemory...),
		TodoItems:     append([]tools.TodoItem(nil), r.loop.TodoItems()...),
		TaskContract:  r.loop.TaskContract(),
		Messages:      r.session.Messages(),
	}
	if err := session.Save(targetState, state); err != nil {
		return "", err
	}
	if err := copyFileIfExists(r.transcriptPath, session.TranscriptPath(r.cfg.StateDir, next)); err != nil {
		return "", err
	}
	if err := copyFileIfExists(r.tracePath, trace.Path(r.cfg.StateDir, next)); err != nil {
		return "", err
	}
	r.setSessionName(next)
	r.setSessionIdentity(state.SessionID, state.ParentSession)
	r.dirtySession = false
	return fmt.Sprintf("forked conversation to %s", next), nil
}

func renameFileIfExists(src, dst string) error {
	if strings.TrimSpace(src) == "" || strings.TrimSpace(dst) == "" || src == dst {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return nil
}

func copyFileIfExists(src, dst string) error {
	if strings.TrimSpace(src) == "" || strings.TrimSpace(dst) == "" || src == dst {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func (r *chatRuntime) planCommand() string {
	_, text, err := readPlanFile(r.cfg.WorkDir)
	if err == nil {
		return text
	}
	if os.IsNotExist(err) {
		return "plan read error: no plan.md found"
	}
	return fmt.Sprintf("plan read error: %v", err)
}

func (r *chatRuntime) contractCommand() string {
	path, text, err := readTaskContractFile(r.cfg.WorkDir)
	if err != nil {
		return fmt.Sprintf("contract read error: %v", err)
	}
	if strings.TrimSpace(text) == "" {
		return "(no task contract)"
	}
	return "path=" + path + "\n" + text
}

func (r *chatRuntime) saveCommand() string {
	record := r.renderSessionRecord()
	path, err := r.writeSessionRecordExport(record)
	if err != nil {
		return fmt.Sprintf("save export error: %v", err)
	}
	return fmt.Sprintf(i18n.T("cmd.save.ok"), path)
}

func (r *chatRuntime) renderSessionRecord() string {
	if r == nil {
		return ""
	}
	lines := []string{
		"# tacli conversation record",
		fmt.Sprintf("conversation=%s", r.sessionName),
		fmt.Sprintf("model=%s", strings.TrimSpace(r.cfg.Model)),
		fmt.Sprintf("workdir=%s", r.cfg.WorkDir),
		fmt.Sprintf("state=%s", r.statePath),
		fmt.Sprintf("transcript=%s", r.transcriptPath),
		fmt.Sprintf("trace=%s", r.tracePath),
		fmt.Sprintf("exported_at=%s", time.Now().UTC().Format(time.RFC3339)),
		"",
		"## messages",
	}
	msgs := r.session.Messages()
	for i, msg := range msgs {
		lines = append(lines, renderSessionMessageRecord(i, msg)...)
	}
	return strings.TrimSpace(strings.Join(lines, "\n")) + "\n"
}

func renderSessionMessageRecord(index int, msg model.Message) []string {
	lines := []string{fmt.Sprintf("### %d %s", index+1, msg.Role)}
	content := strings.TrimSpace(model.ContentString(msg.Content))
	if msg.Role == "system" && index == 0 {
		lines = append(lines, fmt.Sprintf("(initial system prompt omitted, chars=%d)", len(content)))
		return append(lines, "")
	}
	if msg.ToolCallID != "" {
		lines = append(lines, "tool_call_id="+msg.ToolCallID)
	}
	if len(msg.ToolCalls) > 0 {
		for _, call := range msg.ToolCalls {
			name := strings.TrimSpace(call.Function.Name)
			args := strings.TrimSpace(call.Function.Arguments)
			switch {
			case name != "" && args != "":
				lines = append(lines, fmt.Sprintf("tool_call %s %s", name, args))
			case name != "":
				lines = append(lines, "tool_call "+name)
			}
		}
	}
	if content != "" {
		lines = append(lines, content)
	}
	return append(lines, "")
}

func (r *chatRuntime) writeSessionRecordExport(record string) (string, error) {
	path := r.sessionRecordExportPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(record), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func (r *chatRuntime) sessionRecordExportPath() string {
	return "/tmp/session-data.txt"
}

func (r *chatRuntime) hooksCommand(fields []string) string {
	if len(fields) == 1 {
		return strings.Join([]string{
			"PreToolUse:",
			formatHookCommandList(r.cfg.Hooks.PreToolUse),
			"PostToolUse:",
			formatHookCommandList(r.cfg.Hooks.PostToolUse),
		}, "\n")
	}
	switch strings.ToLower(strings.TrimSpace(fields[1])) {
	case "add-pre":
		if len(fields) < 3 {
			return "usage: /hooks add-pre <command>"
		}
		r.cfg.Hooks.PreToolUse = append(r.cfg.Hooks.PreToolUse, strings.TrimSpace(strings.Join(fields[2:], " ")))
	case "add-post":
		if len(fields) < 3 {
			return "usage: /hooks add-post <command>"
		}
		r.cfg.Hooks.PostToolUse = append(r.cfg.Hooks.PostToolUse, strings.TrimSpace(strings.Join(fields[2:], " ")))
	case "remove-pre":
		if len(fields) != 3 {
			return "usage: /hooks remove-pre <index>"
		}
		index, err := strconv.Atoi(strings.TrimSpace(fields[2]))
		if err != nil || index < 1 || index > len(r.cfg.Hooks.PreToolUse) {
			return "invalid PreToolUse index"
		}
		r.cfg.Hooks.PreToolUse = append(r.cfg.Hooks.PreToolUse[:index-1], r.cfg.Hooks.PreToolUse[index:]...)
	case "remove-post":
		if len(fields) != 3 {
			return "usage: /hooks remove-post <index>"
		}
		index, err := strconv.Atoi(strings.TrimSpace(fields[2]))
		if err != nil || index < 1 || index > len(r.cfg.Hooks.PostToolUse) {
			return "invalid PostToolUse index"
		}
		r.cfg.Hooks.PostToolUse = append(r.cfg.Hooks.PostToolUse[:index-1], r.cfg.Hooks.PostToolUse[index:]...)
	case "clear":
		r.cfg.Hooks = tools.HookConfig{}
	default:
		return "usage: /hooks [add-pre <command>|add-post <command>|remove-pre <index>|remove-post <index>|clear]"
	}
	r.rebuildLoop()
	_ = r.save()
	_ = r.pushRemoteSettings()
	return r.hooksCommand([]string{"/hooks"})
}

func (r *chatRuntime) agentsCommand(fields []string) string {
	if r.jobs == nil || r.jobs.orchestration == nil {
		return "no background agents available"
	}
	usage := strings.Join([]string{
		"usage: /agents",
		"usage: /agents list",
		"usage: /agents <id>",
		"usage: /agents cancel <id>",
	}, "\n")
	if len(fields) == 1 || strings.EqualFold(fields[1], "list") {
		return formatSubagentList(r.jobs.orchestration.List())
	}
	if strings.EqualFold(fields[1], "cancel") {
		if len(fields) != 3 {
			return usage
		}
		if err := r.jobs.Cancel(fields[2]); err != nil {
			return err.Error()
		}
		return "canceled " + fields[2]
	}
	if len(fields) != 2 {
		return usage
	}
	snap, ok := r.jobs.orchestration.Snapshot(fields[1])
	if !ok {
		return fmt.Sprintf("unknown agent %q", fields[1])
	}
	return formatSubagentSnapshot(snap)
}

func (r *chatRuntime) mcpCommand(fields []string, input string) string {
	statePath := mcp.Path(filepath.Join(r.cfg.WorkDir, ".tacli"))
	state, err := mcp.Load(statePath)
	if err != nil {
		return "mcp load error: " + err.Error()
	}
	usage := strings.Join([]string{
		"usage: /mcp list",
		"usage: /mcp add <name> <command> [args...]",
		"usage: /mcp remove <name>",
		"usage: /mcp resources <name> [cursor]",
		"usage: /mcp read <name> <uri>",
	}, "\n")
	if len(fields) == 1 || strings.EqualFold(fields[1], "list") {
		if len(state.Servers) == 0 {
			return "no MCP servers configured"
		}
		lines := make([]string, 0, len(state.Servers))
		for _, server := range state.Servers {
			line := fmt.Sprintf("%s transport=%s command=%s", server.Name, firstNonEmpty(server.Transport, "stdio"), server.Command)
			if len(server.Args) > 0 {
				line += " args=" + strings.Join(server.Args, " ")
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n")
	}
	switch strings.ToLower(strings.TrimSpace(fields[1])) {
	case "add":
		if len(fields) < 4 {
			return usage
		}
		server := mcp.Server{
			Name:      strings.TrimSpace(fields[2]),
			Command:   strings.TrimSpace(fields[3]),
			Args:      append([]string(nil), fields[4:]...),
			Transport: "stdio",
		}
		if strings.TrimSpace(server.Name) == "" || strings.TrimSpace(server.Command) == "" {
			return usage
		}
		state = mcp.Upsert(state, server)
		if err := mcp.Save(statePath, state); err != nil {
			return "mcp save error: " + err.Error()
		}
		return fmt.Sprintf("saved MCP server %s", server.Name)
	case "remove", "delete":
		if len(fields) != 3 {
			return usage
		}
		var removed bool
		state, removed = mcp.Remove(state, fields[2])
		if !removed {
			return fmt.Sprintf("unknown MCP server %q", fields[2])
		}
		if err := mcp.Save(statePath, state); err != nil {
			return "mcp save error: " + err.Error()
		}
		return fmt.Sprintf("removed MCP server %s", fields[2])
	case "resources":
		if len(fields) < 3 || len(fields) > 4 {
			return usage
		}
		server, ok := resolveNamedMCPServer(state.Servers, fields[2])
		if !ok {
			return fmt.Sprintf("unknown MCP server %q", fields[2])
		}
		cursor := ""
		if len(fields) == 4 {
			cursor = fields[3]
		}
		result, err := mcp.ListResources(context.Background(), r.cfg.WorkDir, server, cursor)
		if err != nil {
			return "mcp resources error: " + err.Error()
		}
		if len(result.Resources) == 0 {
			return fmt.Sprintf("server=%s resources=0", server.Name)
		}
		lines := []string{fmt.Sprintf("server=%s resources=%d", server.Name, len(result.Resources))}
		for _, resource := range result.Resources {
			line := resource.URI
			if strings.TrimSpace(resource.Name) != "" {
				line += " name=" + resource.Name
			}
			if strings.TrimSpace(resource.MIMEType) != "" {
				line += " mime=" + resource.MIMEType
			}
			if strings.TrimSpace(resource.Description) != "" {
				line += " desc=" + resource.Description
			}
			lines = append(lines, line)
		}
		if strings.TrimSpace(result.NextCursor) != "" {
			lines = append(lines, "next_cursor="+result.NextCursor)
		}
		return strings.Join(lines, "\n")
	case "read":
		if len(fields) < 4 {
			return usage
		}
		server, ok := resolveNamedMCPServer(state.Servers, fields[2])
		if !ok {
			return fmt.Sprintf("unknown MCP server %q", fields[2])
		}
		uri := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), fields[0]+" "+fields[1]+" "+fields[2]))
		result, err := mcp.ReadResource(context.Background(), r.cfg.WorkDir, server, uri)
		if err != nil {
			return "mcp read error: " + err.Error()
		}
		if len(result.Contents) == 0 {
			return fmt.Sprintf("server=%s uri=%s contents=0", server.Name, uri)
		}
		lines := []string{fmt.Sprintf("server=%s uri=%s contents=%d", server.Name, uri, len(result.Contents))}
		for _, item := range result.Contents {
			if strings.TrimSpace(item.Text) != "" {
				lines = append(lines, item.Text)
				continue
			}
			if strings.TrimSpace(item.Blob) != "" {
				lines = append(lines, fmt.Sprintf("[base64 blob] mime=%s bytes=%d", firstNonEmpty(item.MIMEType, "application/octet-stream"), len(item.Blob)))
				continue
			}
			lines = append(lines, "[empty content] uri="+item.URI)
		}
		return strings.Join(lines, "\n")
	default:
		return usage
	}
}

func resolveNamedMCPServer(servers []mcp.Server, name string) (mcp.Server, bool) {
	for _, server := range servers {
		if strings.EqualFold(server.Name, strings.TrimSpace(name)) {
			return server, true
		}
	}
	return mcp.Server{}, false
}

func formatSubagentList(snaps []agent.SubagentSnapshot) string {
	if len(snaps) == 0 {
		return "no background agents"
	}
	lines := make([]string, 0, len(snaps))
	for _, snap := range snaps {
		line := fmt.Sprintf("%s  %s", snap.ID, firstNonEmpty(snap.Status, "unknown"))
		if strings.TrimSpace(snap.Role) != "" {
			line += " role=" + snap.Role
		}
		if strings.TrimSpace(snap.Model) != "" {
			line += " model=" + snap.Model
		}
		if snap.TaskCount > 0 {
			line += fmt.Sprintf(" tasks=%d", snap.TaskCount)
		}
		if snap.Queued > 0 {
			line += fmt.Sprintf(" queued=%d", snap.Queued)
		}
		if snap.Session.MessageCount > 0 {
			line += fmt.Sprintf(" messages=%d", snap.Session.MessageCount)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func formatSubagentSnapshot(snap agent.SubagentSnapshot) string {
	lines := []string{
		fmt.Sprintf("id=%s", snap.ID),
		fmt.Sprintf("status=%s", firstNonEmpty(snap.Status, "unknown")),
	}
	if strings.TrimSpace(snap.Role) != "" {
		lines = append(lines, "role="+snap.Role)
	}
	if strings.TrimSpace(snap.Model) != "" {
		lines = append(lines, "model="+snap.Model)
	}
	if snap.TaskCount > 0 {
		lines = append(lines, fmt.Sprintf("tasks=%d", snap.TaskCount))
	}
	if snap.Queued > 0 {
		lines = append(lines, fmt.Sprintf("queued=%d", snap.Queued))
	}
	if snap.Session.MessageCount > 0 {
		lines = append(lines, fmt.Sprintf("messages=%d", snap.Session.MessageCount))
	}
	if strings.TrimSpace(snap.LastPrompt) != "" {
		lines = append(lines, "last_prompt="+compactJobText(snap.LastPrompt, 180))
	}
	if strings.TrimSpace(snap.LastOutput) != "" {
		lines = append(lines, "last_output="+compactJobText(snap.LastOutput, 180))
	}
	if strings.TrimSpace(snap.LastError) != "" {
		lines = append(lines, "last_error="+compactJobText(snap.LastError, 180))
	}
	if strings.TrimSpace(snap.LogTail) != "" {
		lines = append(lines, "log_tail="+compactJobText(snap.LogTail, 180))
	}
	return strings.Join(lines, "\n")
}

func formatHookCommandList(commands []string) string {
	if len(commands) == 0 {
		return "(none)"
	}
	lines := make([]string, 0, len(commands))
	for i, command := range commands {
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, command))
	}
	return strings.Join(lines, "\n")
}

func (r *chatRuntime) policyCommand(fields []string, input string) string {
	if r.permissions == nil {
		r.permissions = loadRuntimePolicy(r.cfg)
	}
	if r.permissions == nil {
		return "policy store unavailable"
	}
	usage := strings.Join([]string{
		"usage: /policy",
		"usage: /policy default <prompt|read-only|workspace-write|danger-full-access|allow|deny>",
		"usage: /policy tool <name> <prompt|read-only|workspace-write|danger-full-access|allow|deny>",
		"usage: /policy command list",
		"usage: /policy command add <prompt|read-only|workspace-write|danger-full-access|allow|deny> <pattern>",
		"usage: /policy command remove <index>",
		"usage: /policy reload",
		"usage: /policy save",
	}, "\n")
	if len(fields) == 1 || strings.EqualFold(fields[1], "show") || strings.EqualFold(fields[1], "list") {
		return tools.FormatPermissionState(r.permissions.Snapshot())
	}
	switch strings.ToLower(strings.TrimSpace(fields[1])) {
	case "default":
		if len(fields) != 3 {
			return usage
		}
		r.permissions.SetDefault(fields[2])
		r.cfg.ApprovalMode = tools.NormalizePermissionMode(fields[2])
		if r.approver != nil {
			_ = r.approver.SetMode(r.cfg.ApprovalMode)
		}
	case "tool":
		if len(fields) != 4 {
			return usage
		}
		r.permissions.SetToolMode(fields[2], fields[3])
	case "command":
		if len(fields) < 3 {
			return usage
		}
		switch strings.ToLower(strings.TrimSpace(fields[2])) {
		case "list", "show":
			return tools.FormatPermissionState(r.permissions.Snapshot())
		case "add":
			if len(fields) < 5 {
				return usage
			}
			prefix := fields[0] + " " + fields[1] + " " + fields[2] + " " + fields[3]
			pattern := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), prefix))
			if pattern == "" {
				return usage
			}
			r.permissions.SetCommandMode(pattern, fields[3])
		case "remove", "delete":
			if len(fields) != 4 {
				return usage
			}
			index, err := strconv.Atoi(strings.TrimSpace(fields[3]))
			if err != nil || index < 1 || !r.permissions.RemoveCommandRule(index-1) {
				return "invalid command rule index"
			}
		default:
			return usage
		}
	case "reload":
		r.permissions = loadRuntimePolicy(r.cfg)
		if r.permissions == nil {
			return "policy store unavailable"
		}
		r.rebuildLoop()
		return tools.FormatPermissionState(r.permissions.Snapshot())
	case "save":
		if err := r.permissions.Save(); err != nil {
			return "policy save error: " + err.Error()
		}
		_ = r.pushRemoteSettings()
		return tools.FormatPermissionState(r.permissions.Snapshot())
	default:
		return usage
	}
	if err := r.permissions.Save(); err != nil {
		return "policy save error: " + err.Error()
	}
	r.rebuildLoop()
	_ = r.pushRemoteSettings()
	return tools.FormatPermissionState(r.permissions.Snapshot())
}

func (r *chatRuntime) skillsCommand() string {
	return formatSkills(r.skills)
}

func (r *chatRuntime) capabilitiesCommand(fields []string) string {
	if len(fields) == 1 {
		return formatCapabilities(tools.BundledCapabilityPacks())
	}
	if len(fields) == 2 {
		pack, ok := tools.FindCapabilityPack(fields[1])
		if !ok {
			return "unknown capability: " + fields[1]
		}
		return formatCapabilities([]tools.CapabilityPack{pack})
	}
	return "usage: /capabilities [name]"
}

func (r *chatRuntime) tasksCommand(fields []string, input string) string {
	if r == nil || r.taskStore == nil {
		return "task store unavailable"
	}
	usage := strings.Join([]string{
		"usage: /tasks list",
		"usage: /tasks create <title>",
		"usage: /tasks update <id> [status=<pending|in_progress|completed|canceled>] [title=<text>] [details=<text>]",
		"usage: /tasks delete <id>",
	}, "\n")
	if len(fields) == 1 || strings.EqualFold(fields[1], "list") {
		return tasks.Format(r.taskStore.List())
	}
	switch strings.ToLower(strings.TrimSpace(fields[1])) {
	case "create":
		title := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), fields[0]+" "+fields[1]))
		item, err := r.taskStore.Create(title, "")
		if err != nil {
			return err.Error()
		}
		return tasks.Format([]tasks.Item{item})
	case "update":
		if len(fields) < 3 {
			return usage
		}
		update, err := parseTaskUpdateFields(fields[3:])
		if err != nil {
			return err.Error()
		}
		item, err := r.taskStore.Update(fields[2], update)
		if err != nil {
			return err.Error()
		}
		return tasks.Format([]tasks.Item{item})
	case "delete":
		if len(fields) != 3 {
			return usage
		}
		if err := r.taskStore.Delete(fields[2]); err != nil {
			return err.Error()
		}
		return "deleted " + fields[2]
	default:
		return usage
	}
}

func parseTaskUpdateFields(fields []string) (tasks.Update, error) {
	var update tasks.Update
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return tasks.Update{}, fmt.Errorf("invalid update field %q", field)
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "title":
			update.Title = &value
		case "status":
			update.Status = &value
		case "details":
			update.Details = &value
		default:
			return tasks.Update{}, fmt.Errorf("unknown task field %q", key)
		}
	}
	return update, nil
}

func (r *chatRuntime) reviewCommand(fields []string) string {
	opts, err := parseReviewArgs(fields[1:])
	if err != nil {
		return "review usage: /review [base] [target] [--staged] [--path <path>]"
	}
	preview, err := tools.ReviewDiff(context.Background(), r.cfg.WorkDir, opts.base, opts.target, opts.path, opts.staged)
	if err != nil {
		return "review setup error: " + err.Error()
	}
	if strings.TrimSpace(preview) == "no diff found" {
		return "no diff found for the requested review scope"
	}
	preflight, err := buildReviewPreflight(context.Background(), r.cfg.WorkDir, opts)
	if err != nil {
		return "review preflight error: " + err.Error()
	}
	prompt := buildReviewPrompt(opts, preflight)
	output, err := r.executeTask(context.Background(), prompt)
	if err != nil {
		return "review error: " + err.Error()
	}
	return output
}

type reviewOptions struct {
	base   string
	target string
	path   string
	staged bool
}

type reviewPreflight struct {
	changedFiles []string
	diffStat     string
	docsOnly     bool
}

func parseReviewArgs(args []string) (reviewOptions, error) {
	var opts reviewOptions
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch arg {
		case "":
			continue
		case "--staged", "staged":
			opts.staged = true
		case "--path", "path":
			i++
			if i >= len(args) {
				return reviewOptions{}, fmt.Errorf("missing path value")
			}
			opts.path = strings.TrimSpace(args[i])
			if opts.path == "" {
				return reviewOptions{}, fmt.Errorf("missing path value")
			}
		default:
			if opts.base == "" {
				opts.base = arg
				continue
			}
			if opts.target == "" {
				opts.target = arg
				continue
			}
			return reviewOptions{}, fmt.Errorf("too many positional args")
		}
	}
	if opts.base == "" {
		opts.base = "HEAD"
	}
	return opts, nil
}

func buildReviewPrompt(opts reviewOptions, preflight reviewPreflight) string {
	scope := []string{"Review the current git diff"}
	if opts.staged {
		scope = append(scope, "for staged changes")
	}
	if opts.base != "" {
		scope = append(scope, "against "+opts.base)
	}
	if opts.target != "" {
		scope = append(scope, "to "+opts.target)
	}
	if opts.path != "" {
		scope = append(scope, "scoped to "+opts.path)
	}
	lines := []string{
		strings.Join(scope, " ") + ".",
		"Preflight facts:",
		fmt.Sprintf("- staged=%t", opts.staged),
		fmt.Sprintf("- base=%s", firstNonEmpty(opts.base, "(none)")),
	}
	if strings.TrimSpace(opts.target) != "" {
		lines = append(lines, "- target="+opts.target)
	}
	if strings.TrimSpace(opts.path) != "" {
		lines = append(lines, "- path="+opts.path)
	}
	if len(preflight.changedFiles) > 0 {
		lines = append(lines, fmt.Sprintf("- changed_files(%d)=%s", len(preflight.changedFiles), strings.Join(preflight.changedFiles, ", ")))
	}
	if strings.TrimSpace(preflight.diffStat) != "" {
		lines = append(lines, "- diff_stat="+preflight.diffStat)
	}
	lines = append(lines, fmt.Sprintf("- docs_only=%t", preflight.docsOnly))
	lines = append(lines, "- local_verification=not run automatically by /review")
	lines = append(lines, "Use review_diff first with the same scope. Ground findings in the actual patch. Focus on bugs, regressions, risky assumptions, and missing tests. Present findings first with file references when possible. If there are no findings, say so explicitly.")
	return strings.Join(lines, "\n")
}

func buildReviewPreflight(ctx context.Context, workDir string, opts reviewOptions) (reviewPreflight, error) {
	filesText, err := runReviewGit(ctx, workDir, opts, "--name-only")
	if err != nil {
		return reviewPreflight{}, err
	}
	files := splitNonEmptyLines(filesText)
	statText, err := runReviewGit(ctx, workDir, opts, "--stat", "--compact-summary")
	if err != nil {
		return reviewPreflight{}, err
	}
	return reviewPreflight{
		changedFiles: files,
		diffStat:     compactJobText(strings.TrimSpace(statText), 400),
		docsOnly:     onlyDocLikeFiles(files),
	}, nil
}

func runReviewGit(ctx context.Context, workDir string, opts reviewOptions, extraArgs ...string) (string, error) {
	args := []string{"-C", workDir, "diff", "--no-ext-diff"}
	args = append(args, extraArgs...)
	if opts.staged {
		args = append(args, "--cached")
	}
	if opts.target != "" {
		args = append(args, opts.base, opts.target)
	} else if !opts.staged {
		args = append(args, opts.base)
	}
	if strings.TrimSpace(opts.path) != "" {
		args = append(args, "--", opts.path)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	data, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(data))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return "", fmt.Errorf("git diff failed: %s", text)
	}
	return text, nil
}

func splitNonEmptyLines(text string) []string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func onlyDocLikeFiles(files []string) bool {
	if len(files) == 0 {
		return false
	}
	for _, file := range files {
		lower := strings.ToLower(strings.TrimSpace(file))
		switch {
		case strings.HasSuffix(lower, ".md"),
			strings.HasSuffix(lower, ".txt"),
			strings.HasSuffix(lower, ".rst"),
			strings.HasSuffix(lower, ".adoc"),
			strings.HasSuffix(lower, ".png"),
			strings.HasSuffix(lower, ".jpg"),
			strings.HasSuffix(lower, ".jpeg"),
			strings.HasSuffix(lower, ".gif"),
			strings.HasSuffix(lower, ".svg"),
			strings.Contains(lower, "/docs/"),
			strings.HasPrefix(lower, "docs/"):
			continue
		default:
			return false
		}
	}
	return true
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
	switch strings.ToLower(strings.TrimSpace(fields[1])) {
	case "load":
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
	case "unload":
		if len(fields) < 3 {
			return "usage: /plugin unload <name-or-path>"
		}
		target := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), fields[0]+" "+fields[1]))
		loaded, ok, err := r.pluginManager.Unload(target)
		if err != nil {
			return "plugin unload error: " + err.Error()
		}
		if !ok {
			return fmt.Sprintf("plugin %q is not loaded", target)
		}
		r.rebuildLoop()
		meta := loaded.Plugin.Metadata()
		if strings.TrimSpace(meta.Name) == "" {
			meta.Name = loaded.Descriptor.Name
		}
		return fmt.Sprintf("unloaded plugin %s", meta.Name)
	default:
		return "usage: /plugin [list|load <name-or-path>|unload <name-or-path>]"
	}
}

func (r *chatRuntime) reloadPluginsCommand() string {
	if r.pluginManager == nil {
		return "plugin manager unavailable"
	}
	reloaded, err := r.pluginManager.ReloadLoaded()
	if err != nil {
		return "plugin reload error: " + err.Error()
	}
	r.rebuildLoop()
	if len(reloaded) == 0 {
		return "reloaded 0 plugins"
	}
	names := make([]string, 0, len(reloaded))
	for _, item := range reloaded {
		name := item.Descriptor.Name
		if meta := item.Plugin.Metadata(); strings.TrimSpace(meta.Name) != "" {
			name = meta.Name
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return fmt.Sprintf("reloaded %d plugins: %s", len(names), strings.Join(names, ", "))
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
		return r.describeMemory()
	}
	switch strings.ToLower(strings.TrimSpace(fields[1])) {
	case "list":
		return r.describeMemory()
	case "save":
		if err := r.saveMemory(); err != nil {
			return "memory save error: " + err.Error()
		}
		return r.describeMemory()
	case "reload", "load":
		if err := r.reloadMemory(); err != nil {
			return "memory reload error: " + err.Error()
		}
		return r.describeMemory()
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
	case "team":
		return r.teamMemoryCommand(fields[2:], input)
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
	case "clear-project":
		r.projectMemory = nil
		r.refreshMemoryContext()
		_ = r.saveMemory()
		_ = r.save()
		return r.describeMemory()
	default:
		return "usage: /memory [show|list|save|reload|remember <text>|remember-global <text>|forget <query>|forget-global <query>|clear-project]"
	}
	r.refreshMemoryContext()
	_ = r.saveMemory()
	_ = r.save()
	return r.describeMemory()
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
		Role: "user",
		Content: "<system-reminder>internal-orchestration\nBackground exploration job " + jobID +
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
		return memory.FormatNotes(r.globalMemory, r.teamMemory, r.projectMemory), nil
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
	mode := tools.NormalizePermissionMode(cfg.ApprovalMode)
	if jobs == nil || (mode != tools.PermissionModeDangerFullAccess && mode != tools.PermissionModeAllow) {
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

func isEmptyTaskContract(contract tools.TaskContract) bool {
	return strings.TrimSpace(contract.Objective) == "" &&
		len(contract.Deliverables) == 0 &&
		len(contract.AcceptanceChecks) == 0
}

func (r *chatRuntime) applyPlanningState(items []tools.TodoItem, contract tools.TaskContract) {
	if r == nil || r.loop == nil {
		return
	}
	_ = r.loop.ReplaceTodo(items)
	if isEmptyTaskContract(contract) {
		_ = r.loop.ClearTaskContract()
	} else {
		_ = r.loop.ReplaceTaskContract(contract)
	}
	if r.session != nil {
		r.refreshMemoryContext()
	}
}

func (r *chatRuntime) clearPlanningState() {
	r.applyPlanningState(nil, tools.TaskContract{})
}

func (r *chatRuntime) refreshModelMetadata() {
	if r == nil {
		return
	}
	r.modelContextWindow = 0
	client := harness.NewFactory(r.cfg).NewModelClient()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, ok, err := client.ModelInfo(ctx, r.cfg.Model)
	if err != nil || !ok {
		return
	}
	if info.ContextWindow > 0 {
		r.modelContextWindow = info.ContextWindow
	}
}

func (r *chatRuntime) newSession() *agent.Session {
	return newRuntimeKernel(r.cfg, r.approver, os.Stderr, nil, r.permissions).newSession(r.loop, "chat", r.renderSystemMemory())
}

func (r *chatRuntime) injectJobSummary(snap jobSnapshot) {
	msgs := r.session.Messages()
	msgs = append(msgs, model.Message{
		Role:    "user",
		Content: "<system-reminder>background-result\nBackground result available for the current task. Use it as additional context if relevant:\n" + summarizeJobForSession(snap),
	})
	r.session.ReplaceMessages(msgs)
	r.dirtySession = true
}

func (r *chatRuntime) refreshMemoryContext() {
	if r.jobs != nil {
		r.jobs.UpdateMemory(r.renderSystemMemory())
	}
	r.session = newRuntimeKernel(r.cfg, r.approver, os.Stderr, nil, r.permissions).refreshSessionPrompt(r.session, r.loop, "chat", r.renderSystemMemory())
}

func (r *chatRuntime) reloadMemory() error {
	state, err := memory.Load(r.memoryPath)
	if err != nil {
		if os.IsNotExist(err) {
			r.globalMemory = nil
			r.teamMemory = nil
			r.projectMemory = nil
			r.refreshMemoryContext()
			return nil
		}
		return err
	}
	r.globalMemory = append([]string(nil), state.Global...)
	r.teamMemory = append([]string(nil), state.Teams[r.teamKey]...)
	r.projectMemory = append([]string(nil), state.Projects[r.scopeKey]...)
	r.refreshMemoryContext()
	return nil
}

func (r *chatRuntime) describeMemory() string {
	text := memory.FormatNotes(r.globalMemory, r.teamMemory, r.projectMemory)
	summary := memory.Summarize(memory.State{
		Global:   r.globalMemory,
		Teams:    map[string][]string{r.teamKey: r.teamMemory},
		Projects: map[string][]string{r.scopeKey: r.projectMemory},
	}, r.teamKey, r.scopeKey)
	lines := []string{
		fmt.Sprintf("path=%s", r.memoryPath),
		fmt.Sprintf("team=%s", firstNonEmpty(r.teamKey, "(none)")),
		fmt.Sprintf("scope=%s", r.scopeKey),
		fmt.Sprintf("global_notes=%d", summary.GlobalCount),
		fmt.Sprintf("team_notes=%d", summary.TeamCount),
		fmt.Sprintf("project_notes=%d", summary.ProjectCount),
		text,
	}
	return strings.Join(lines, "\n")
}

func (r *chatRuntime) save() error {
	return session.Save(r.statePath, session.State{
		SessionID:     r.sessionID,
		ParentSession: r.parentSession,
		SessionName:   r.sessionName,
		Model:         r.cfg.Model,
		OutputMode:    r.outputMode,
		ApprovalMode:  r.cfg.ApprovalMode,
		TeamKey:       r.teamKey,
		ScopeKey:      r.scopeKey,
		GlobalMemory:  append([]string(nil), r.globalMemory...),
		TeamMemory:    append([]string(nil), r.teamMemory...),
		ProjectMemory: append([]string(nil), r.projectMemory...),
		TodoItems:     append([]tools.TodoItem(nil), r.loop.TodoItems()...),
		TaskContract:  r.loop.TaskContract(),
		Messages:      r.session.Messages(),
	})
}

func (r *chatRuntime) saveMemory() error {
	state := memory.State{
		Global: r.globalMemory,
		Teams: map[string][]string{
			r.teamKey: append([]string(nil), r.teamMemory...),
		},
		Projects: map[string][]string{
			r.scopeKey: append([]string(nil), r.projectMemory...),
		},
	}
	if len(r.teamMemory) == 0 || strings.TrimSpace(r.teamKey) == "" {
		state.Teams = nil
	}
	if len(r.projectMemory) == 0 {
		state.Projects = nil
	}
	if existing, err := memory.Load(r.memoryPath); err == nil {
		state = memory.Merge(existing, state)
		if len(r.teamMemory) == 0 || strings.TrimSpace(r.teamKey) == "" {
			state = memory.DeleteTeamScope(state, r.teamKey)
		}
		if len(r.projectMemory) == 0 {
			state = memory.DeleteScope(state, r.scopeKey)
		}
	}
	return memory.Save(r.memoryPath, state)
}

func (r *chatRuntime) pullRemoteSettings() error {
	if r == nil || !r.cfg.SettingsSync || strings.TrimSpace(r.cfg.SettingsURL) == "" {
		return nil
	}
	ctx, cancel := config.SettingsSyncContext(context.Background())
	defer cancel()
	settings, err := config.PullSettings(ctx, r.cfg.SettingsURL)
	if err != nil {
		return err
	}
	r.cfg.ApplySettings(settings)
	if r.permissions == nil {
		r.permissions = loadRuntimePolicy(r.cfg)
	}
	if r.permissions != nil {
		r.permissions.Replace(settings.Permissions)
		_ = r.permissions.Save()
	}
	return nil
}

func (r *chatRuntime) pushRemoteSettings() error {
	if r == nil || !r.cfg.SettingsSync || strings.TrimSpace(r.cfg.SettingsURL) == "" {
		return nil
	}
	ctx, cancel := config.SettingsSyncContext(context.Background())
	defer cancel()
	var permissions tools.PermissionState
	if r.permissions != nil {
		permissions = r.permissions.Snapshot()
	}
	return config.PushSettings(ctx, r.cfg.SettingsURL, config.SnapshotSettings(r.cfg, permissions))
}

func (r *chatRuntime) teamMemoryCommand(fields []string, input string) string {
	if strings.TrimSpace(r.teamKey) == "" {
		return "team memory is unavailable; set AGENT_TEAM or configure a git origin remote"
	}
	if len(fields) == 0 || strings.EqualFold(fields[0], "show") || strings.EqualFold(fields[0], "list") {
		return r.describeMemory()
	}
	switch strings.ToLower(strings.TrimSpace(fields[0])) {
	case "remember":
		if len(fields) < 2 {
			return "usage: /memory team remember <text>"
		}
		r.teamMemory = memory.Add(r.teamMemory, memoryCommandBody(input, "/memory team remember"))
	case "forget":
		if len(fields) < 2 {
			return "usage: /memory team forget <query>"
		}
		var removed int
		r.teamMemory, removed = memory.ForgetMatching(r.teamMemory, memoryCommandBody(input, "/memory team forget"))
		r.refreshMemoryContext()
		_ = r.saveMemory()
		_ = r.save()
		return fmt.Sprintf("removed %d team memory note(s)", removed)
	case "clear":
		r.teamMemory = nil
	default:
		return "usage: /memory team [show|remember <text>|forget <query>|clear]"
	}
	r.refreshMemoryContext()
	_ = r.saveMemory()
	_ = r.save()
	return r.describeMemory()
}

func (r *chatRuntime) renderSystemMemory() string {
	return memory.RenderSystemMemory(r.globalMemory, r.teamMemory, r.projectMemory)
}

func memoryCommandBody(input string, prefix string) string {
	trimmed := strings.TrimSpace(input)
	if len(trimmed) <= len(prefix) {
		return ""
	}
	return strings.TrimSpace(trimmed[len(prefix):])
}

func (r *chatRuntime) beforeExit(allowAutoMemory bool) {
	if allowAutoMemory && r.autoMemoryExit && r.dirtySession {
		if added, err := r.summarizeMemoryBestEffort(); err != nil {
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
	return r.applyMemoryNotes(lines)
}

func (r *chatRuntime) summarizeMemoryBestEffort() (int, error) {
	lines, err := r.collectMemoryNotesBestEffort()
	if err != nil {
		return 0, err
	}
	return r.applyMemoryNotes(lines)
}

func (r *chatRuntime) applyMemoryNotes(lines []string) (int, error) {
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

func (r *chatRuntime) collectMemoryNotesBestEffort() ([]string, error) {
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
		r.recordTrace(context.Background(), "runtime", "auto_memory_fallback", map[string]any{
			"error": err.Error(),
		})
		return nil, nil
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

func resolveTeamKey(cfg config.Config) string {
	if strings.TrimSpace(cfg.Team) != "" {
		return strings.TrimSpace(cfg.Team)
	}
	return gitRemoteOwner(cfg.WorkDir)
}

func gitRemoteOwner(workDir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", workDir, "config", "--get", "remote.origin.url")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return remoteOwnerFromURL(string(out))
}

func remoteOwnerFromURL(raw string) string {
	url := strings.TrimSpace(raw)
	if url == "" {
		return ""
	}
	url = strings.TrimSuffix(url, ".git")
	url = strings.ReplaceAll(url, "\\", "/")
	if idx := strings.Index(url, ":"); idx >= 0 && !strings.Contains(url[:idx], "/") {
		url = url[idx+1:]
	}
	parts := strings.Split(url, "/")
	if len(parts) < 2 {
		return ""
	}
	return strings.TrimSpace(parts[len(parts)-2])
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
		fs.StringVar(&opts.outputMode, "output", opts.outputMode, "output mode (raw|json|jsonl|structured)")
	case "chat":
		fs.BoolVar(&dangerously, "dangerously", false, "start chat in dangerously mode")
		fs.StringVar(&opts.conversation, "resume", "", "conversation name to resume or create")
		fs.StringVar(&opts.outputMode, "output", opts.outputMode, "output mode (terminal|raw)")
	}

	if err := fs.Parse(args); err != nil {
		return cfg, runtimeOptions{}, nil, nil, err
	}
	if strings.TrimSpace(os.Getenv("AGENT_STATE_DIR")) == "" {
		cfg.StateDir = config.DefaultStateDir(cfg.WorkDir)
	}
	if dangerously {
		cfg.ApprovalMode = tools.PermissionModeDangerFullAccess
	}
	if err := cfg.Validate(); err != nil {
		return cfg, runtimeOptions{}, nil, nil, err
	}

	cfg.ApprovalMode = tools.NormalizePermissionMode(cfg.ApprovalMode)
	switch cfg.ApprovalMode {
	case tools.PermissionModePrompt, tools.PermissionModeReadOnly, tools.PermissionModeWorkspaceWrite, tools.PermissionModeDangerFullAccess, tools.PermissionModeAllow, tools.PermissionModeDeny:
	default:
		return cfg, runtimeOptions{}, nil, nil, fmt.Errorf("invalid approval mode %q", cfg.ApprovalMode)
	}
	opts.outputMode = transport.NormalizeOutputMode(opts.outputMode)
	if opts.outputMode == "" {
		return cfg, runtimeOptions{}, nil, nil, fmt.Errorf("invalid output mode")
	}
	if name == "chat" && transport.IsStructuredMode(opts.outputMode) {
		return cfg, runtimeOptions{}, nil, nil, fmt.Errorf("structured output mode is only supported for run")
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
	return factory.NewAgent(approver, os.Stderr, nil, loadRuntimePolicy(cfg)), approver
}

func buildAgentWith(cfg config.Config, approver tools.Approver, log io.Writer, jobs tools.JobControl, policy *tools.PermissionStore, extraAuditSinks ...tools.ToolAuditSink) *agent.Agent {
	return newRuntimeKernel(cfg, approver, log, jobs, policy, extraAuditSinks...).buildAgent()
}

func loadRuntimePolicy(cfg config.Config) *tools.PermissionStore {
	store, err := tools.LoadPermissionStore(tools.PermissionPath(cfg.StateDir))
	if err != nil {
		return nil
	}
	return store
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

func (r *chatRuntime) debugToolCallCommand(fields []string) string {
	mode := "latest"
	limit := 1
	if len(fields) > 1 {
		switch fields[1] {
		case "tail":
			mode = "tail"
			limit = 5
			if len(fields) >= 3 {
				if v, err := strconv.Atoi(fields[2]); err == nil && v > 0 {
					limit = min(v, 20)
				}
			}
		case "errors":
			mode = "errors"
			limit = 5
			if len(fields) >= 3 {
				if v, err := strconv.Atoi(fields[2]); err == nil && v > 0 {
					limit = min(v, 20)
				}
			}
		case "replay":
			return r.debugToolCallReplay()
		default:
			return i18n.T("cmd.debugtool.usage")
		}
	}
	events, err := tools.ReadAuditTail(r.auditPath, max(20, limit*4))
	if err != nil {
		return "audit read error: " + err.Error()
	}
	if len(events) == 0 {
		return i18n.T("cmd.audit.empty")
	}

	selected := make([]tools.ToolAuditEvent, 0, limit)
	switch mode {
	case "latest":
		selected = append(selected, events[len(events)-1])
	case "tail":
		if len(events) > limit {
			events = events[len(events)-limit:]
		}
		selected = append(selected, events...)
	case "errors":
		for i := len(events) - 1; i >= 0; i-- {
			event := events[i]
			if strings.TrimSpace(event.Status) == "" || event.Status == "ok" {
				continue
			}
			selected = append(selected, event)
			if len(selected) >= limit {
				break
			}
		}
		if len(selected) == 0 {
			return i18n.T("cmd.audit.no_errors")
		}
		for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
			selected[i], selected[j] = selected[j], selected[i]
		}
	}

	blocks := make([]string, 0, len(selected))
	for idx, event := range selected {
		lines := []string{
			fmt.Sprintf("[%d]", idx+1),
			"tool=" + event.Tool,
			"status=" + firstNonEmpty(event.Status, "unknown"),
		}
		if event.DurationMs > 0 {
			lines = append(lines, fmt.Sprintf("duration_ms=%d", event.DurationMs))
		}
		if !event.Time.IsZero() {
			lines = append(lines, "at="+event.Time.Format("2006-01-02T15:04:05Z07:00"))
		}
		if strings.TrimSpace(event.ArgsPreview) != "" {
			lines = append(lines, "args="+compactJobText(event.ArgsPreview, 400))
		}
		if strings.TrimSpace(event.OutputSample) != "" {
			lines = append(lines, "output="+compactJobText(event.OutputSample, 400))
		}
		if strings.TrimSpace(event.Error) != "" {
			lines = append(lines, "error="+compactJobText(event.Error, 400))
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	return strings.Join(blocks, "\n\n")
}

func (r *chatRuntime) debugToolCallReplay() string {
	events, err := tools.ReadAuditTail(r.auditPath, 1)
	if err != nil {
		return "audit read error: " + err.Error()
	}
	if len(events) == 0 {
		return i18n.T("cmd.audit.empty")
	}
	event := events[len(events)-1]
	inputJSON := strings.TrimSpace(event.InputJSON)
	if inputJSON == "" {
		return "latest tool call does not have stored input_json"
	}
	registry := tools.NewRegistryWithOptions(r.cfg.WorkDir, r.cfg.Shell, r.cfg.CommandTimeout, r.approver, r.cfg.Hooks, r.permissions)
	registry.SetAuditSink(tools.NewFileAuditSink(r.auditPath))
	output, callErr := registry.Call(context.Background(), event.Tool, json.RawMessage(inputJSON))
	lines := []string{
		"replayed tool=" + event.Tool,
		"input_json=" + compactJobText(inputJSON, 400),
	}
	if strings.TrimSpace(output) != "" {
		lines = append(lines, "output="+compactJobText(output, 1200))
	}
	if callErr != nil {
		lines = append(lines, "error="+compactJobText(callErr.Error(), 400))
	}
	return strings.Join(lines, "\n")
}

func (r *chatRuntime) debugTurnCommand(fields []string) string {
	mode := "latest"
	limit := 1
	if len(fields) > 1 {
		switch fields[1] {
		case "tail":
			mode = "tail"
			limit = 5
			if len(fields) >= 3 {
				if v, err := strconv.Atoi(fields[2]); err == nil && v > 0 {
					limit = min(v, 20)
				}
			}
		default:
			return i18n.T("cmd.debugturn.usage")
		}
	}
	if r == nil || r.session == nil {
		return "turn summary unavailable"
	}
	turns := r.session.TurnSummaries()
	if len(turns) == 0 {
		return "no turn summaries yet"
	}
	selected := turns
	if mode == "latest" {
		selected = turns[len(turns)-1:]
	} else if len(turns) > limit {
		selected = turns[len(turns)-limit:]
	}
	blocks := make([]string, 0, len(selected))
	for idx, turn := range selected {
		lines := []string{
			fmt.Sprintf("[%d]", idx+1),
			fmt.Sprintf("turn=%d", turn.Turn),
			"decision=" + firstNonEmpty(turn.Decision, "unknown"),
		}
		if names := turnToolNames(turn.Assistant.ToolCalls); len(names) > 0 {
			lines = append(lines, "tools="+strings.Join(names, ","))
		}
		if text := strings.TrimSpace(agent.FormatTerminalOutput(modelContent(turn.Assistant.Content))); text != "" {
			lines = append(lines, "assistant="+compactJobText(text, 400))
		}
		if len(turn.ToolResults) > 0 {
			lines = append(lines, fmt.Sprintf("tool_results=%d", len(turn.ToolResults)))
			for i, msg := range turn.ToolResults {
				text := strings.TrimSpace(agent.FormatTerminalOutput(modelContent(msg.Content)))
				if text == "" {
					continue
				}
				lines = append(lines, fmt.Sprintf("tool_output_%d=%s", i+1, compactJobText(text, 300)))
			}
		}
		if strings.TrimSpace(turn.Reminder) != "" {
			lines = append(lines, "reminder="+compactJobText(turn.Reminder, 240))
		}
		if strings.TrimSpace(turn.Final) != "" {
			lines = append(lines, "final="+compactJobText(turn.Final, 240))
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	return strings.Join(blocks, "\n\n")
}

func (r *chatRuntime) debugRuntimeCommand() string {
	if r == nil || r.session == nil {
		return "runtime stats unavailable"
	}
	stats := r.session.RuntimeStats()
	lines := []string{
		fmt.Sprintf("turns=%d", stats.Turns),
		fmt.Sprintf("assistant_calls=%d", stats.AssistantCalls),
		fmt.Sprintf("tool_calls=%d", stats.ToolCalls),
		fmt.Sprintf("tool_errors=%d", stats.ToolErrors),
		fmt.Sprintf("compactions=%d", stats.Compactions),
		fmt.Sprintf("retry_compactions=%d", stats.RetryCompactions),
		fmt.Sprintf("context_retries=%d", stats.ContextRetries),
		fmt.Sprintf("token_scale=%.3f", stats.TokenScale),
	}
	if stats.LastPromptTokens > 0 {
		lines = append(lines, fmt.Sprintf("last_prompt_tokens=%d", stats.LastPromptTokens))
	}
	if stats.LastPromptApprox > 0 {
		lines = append(lines, fmt.Sprintf("last_prompt_approx=%d", stats.LastPromptApprox))
	}
	if stats.LastUsageAtTurn > 0 {
		lines = append(lines, fmt.Sprintf("last_usage_turn=%d", stats.LastUsageAtTurn))
	}
	return strings.Join(lines, "\n")
}

func turnToolNames(calls []model.ToolCall) []string {
	if len(calls) == 0 {
		return nil
	}
	names := make([]string, 0, len(calls))
	seen := make(map[string]bool, len(calls))
	for _, call := range calls {
		name := strings.TrimSpace(call.Function.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
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
	fmt.Fprintf(os.Stderr, "tacli %s\n\n", version)
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  tacli                 # default chat on interactive terminals")
	fmt.Fprintln(os.Stderr, "  tacli -d              # default chat in dangerously mode")
	fmt.Fprintln(os.Stderr, "  tacli chat")
	fmt.Fprintln(os.Stderr, "  tacli init [--workdir <path>]")
	fmt.Fprintln(os.Stderr, "  tacli plan [--workdir <path>]")
	fmt.Fprintln(os.Stderr, "  tacli run [--dangerously] <task>")
	fmt.Fprintln(os.Stderr, "  tacli status [--workdir <path>]")
	fmt.Fprintln(os.Stderr, "  tacli skills [--workdir <path>]")
	fmt.Fprintln(os.Stderr, "  tacli capabilities [--workdir <path>] [name]")
	fmt.Fprintln(os.Stderr, "  tacli \"<task>\"        # shorthand for run")
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
	fmt.Fprintln(os.Stderr, `  tacli chat --resume bugfix`)
}
