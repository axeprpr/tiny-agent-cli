package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"onek-agent/internal/agent"
	"onek-agent/internal/config"
	"onek-agent/internal/memory"
	"onek-agent/internal/model/openaiapi"
	"onek-agent/internal/session"
	"onek-agent/internal/tools"
)

var version = "dev"

type runtimeOptions struct {
	outputMode string
	session    string
}

type chatRuntime struct {
	cfg            config.Config
	reader         *bufio.Reader
	approver       *tools.TerminalApprover
	loop           *agent.Agent
	session        *agent.Session
	sessionName    string
	outputMode     string
	transcriptPath string
	statePath      string
	memoryPath     string
	memories       []string
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 2
	}

	switch args[0] {
	case "run":
		return runTask(args[1:])
	case "chat":
		return runChat(args[1:])
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
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		printUsage()
		return 2
	}
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
		fmt.Fprintf(os.Stderr, "Interactive mode. session=%s output=%s approval=%s model=%s\n", runtime.sessionName, runtime.outputMode, runtime.approver.Mode(), runtime.cfg.Model)
		fmt.Fprintln(os.Stderr, "Type /help for commands.")
	}

	for {
		if interactive {
			fmt.Fprint(os.Stderr, "onek> ")
		}

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
			handled, exitCode := runtime.handleCommand(task)
			if handled {
				if exitCode >= 0 {
					return exitCode
				}
				if readErr != nil {
					return 0
				}
				continue
			}
		}

		_ = session.AppendTranscript(runtime.transcriptPath, "user", task)
		result, err := runtime.session.RunTask(context.Background(), task)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
			_ = session.AppendTranscript(runtime.transcriptPath, "error", err.Error())
		} else {
			output := formatRunOutput(result.Final, runtime.outputMode)
			fmt.Println(output)
			_ = session.AppendTranscript(runtime.transcriptPath, "assistant", output)
			_ = runtime.save()
		}

		if readErr != nil {
			return 0
		}
	}
}

func newChatRuntime(cfg config.Config, opts runtimeOptions, reader *bufio.Reader) (*chatRuntime, error) {
	loop, approver := buildAgent(cfg, reader)
	sessionName := opts.session
	if sessionName == "" {
		sessionName = "default"
	}

	r := &chatRuntime{
		cfg:            cfg,
		reader:         reader,
		approver:       approver,
		loop:           loop,
		sessionName:    sessionName,
		outputMode:     opts.outputMode,
		transcriptPath: session.TranscriptPath(cfg.StateDir, sessionName),
		statePath:      session.SessionPath(cfg.StateDir, sessionName),
		memoryPath:     memory.Path(cfg.StateDir),
	}
	if mem, err := memory.Load(r.memoryPath); err == nil {
		r.memories = mem.Notes
	}
	r.session = r.newSession()

	if state, err := session.Load(r.statePath); err == nil {
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
		if strings.TrimSpace(state.Model) != "" {
			r.cfg.Model = state.Model
			r.rebuildLoop()
			_ = r.approver.SetMode(r.cfg.ApprovalMode)
		}
	}

	return r, nil
}

func (r *chatRuntime) handleCommand(input string) (bool, int) {
	fields := strings.Fields(strings.TrimSpace(input))
	if len(fields) == 0 {
		return true, -1
	}

	switch fields[0] {
	case "/exit", "/quit":
		_ = r.save()
		return true, 0
	case "/reset":
		r.session = r.newSession()
		_ = r.save()
		fmt.Fprintln(os.Stderr, "context reset")
		return true, -1
	case "/help":
		fmt.Fprintln(os.Stderr, "/help                     show commands")
		fmt.Fprintln(os.Stderr, "/exit                     quit")
		fmt.Fprintln(os.Stderr, "/reset                    clear conversation context")
		fmt.Fprintln(os.Stderr, "/status                   show session settings")
		fmt.Fprintln(os.Stderr, "/approval confirm|dangerously")
		fmt.Fprintln(os.Stderr, "/output raw|terminal")
		fmt.Fprintln(os.Stderr, "/model <name>")
		fmt.Fprintln(os.Stderr, "/memory                    show saved memory")
		fmt.Fprintln(os.Stderr, "/remember <text>           add a memory note")
		fmt.Fprintln(os.Stderr, "/forget <query>            remove matching memory notes")
		return true, -1
	case "/status":
		fmt.Fprintf(os.Stderr, "session=%s\n", r.sessionName)
		fmt.Fprintf(os.Stderr, "model=%s\n", r.cfg.Model)
		fmt.Fprintf(os.Stderr, "approval=%s\n", r.approver.Mode())
		fmt.Fprintf(os.Stderr, "output=%s\n", r.outputMode)
		fmt.Fprintf(os.Stderr, "memory_notes=%d\n", len(r.memories))
		fmt.Fprintf(os.Stderr, "state=%s\n", r.statePath)
		fmt.Fprintf(os.Stderr, "transcript=%s\n", r.transcriptPath)
		fmt.Fprintf(os.Stderr, "memory=%s\n", r.memoryPath)
		return true, -1
	case "/approval":
		if len(fields) != 2 {
			fmt.Fprintln(os.Stderr, "usage: /approval confirm|dangerously")
			return true, -1
		}
		if err := r.approver.SetMode(fields[1]); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			return true, -1
		}
		r.cfg.ApprovalMode = r.approver.Mode()
		fmt.Fprintf(os.Stderr, "approval mode set to %s\n", r.approver.Mode())
		_ = r.save()
		return true, -1
	case "/output":
		if len(fields) != 2 || (fields[1] != "raw" && fields[1] != "terminal") {
			fmt.Fprintln(os.Stderr, "usage: /output raw|terminal")
			return true, -1
		}
		r.outputMode = fields[1]
		fmt.Fprintf(os.Stderr, "output mode set to %s\n", r.outputMode)
		_ = r.save()
		return true, -1
	case "/model":
		if len(fields) < 2 {
			fmt.Fprintln(os.Stderr, "usage: /model <name>")
			return true, -1
		}
		r.cfg.Model = strings.Join(fields[1:], " ")
		r.rebuildLoop()
		fmt.Fprintf(os.Stderr, "model set to %s\n", r.cfg.Model)
		_ = r.save()
		return true, -1
	case "/memory":
		fmt.Fprintln(os.Stderr, memory.FormatNotes(r.memories))
		return true, -1
	case "/remember":
		if len(fields) < 2 {
			fmt.Fprintln(os.Stderr, "usage: /remember <text>")
			return true, -1
		}
		r.memories = memory.Add(r.memories, strings.TrimSpace(input[len("/remember"):]))
		fmt.Fprintln(os.Stderr, "memory saved")
		r.refreshMemoryContext()
		_ = r.saveMemory()
		_ = r.save()
		return true, -1
	case "/forget":
		if len(fields) < 2 {
			fmt.Fprintln(os.Stderr, "usage: /forget <query>")
			return true, -1
		}
		updated, removed := memory.ForgetMatching(r.memories, strings.TrimSpace(input[len("/forget"):]))
		r.memories = updated
		fmt.Fprintf(os.Stderr, "removed %d memory note(s)\n", removed)
		r.refreshMemoryContext()
		_ = r.saveMemory()
		_ = r.save()
		return true, -1
	default:
		return false, -1
	}
}

func (r *chatRuntime) rebuildLoop() {
	loop, approver := buildAgent(r.cfg, r.reader)
	r.loop = loop
	r.approver = approver
	r.session.SetAgent(loop)
	_ = r.approver.SetMode(r.cfg.ApprovalMode)
}

func (r *chatRuntime) newSession() *agent.Session {
	return r.loop.NewSessionWithMemory(memory.RenderSystemMemory(r.memories))
}

func (r *chatRuntime) refreshMemoryContext() {
	messages := r.session.Messages()
	if len(messages) == 0 {
		r.session = r.newSession()
		return
	}
	messages[0].Content = agent.SystemPromptWithMemory(memory.RenderSystemMemory(r.memories))
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
	return memory.Save(r.memoryPath, memory.State{Notes: r.memories})
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
		outputMode: "raw",
		session:    "default",
	}
	dangerously := false

	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.BaseURL, "base-url", cfg.BaseURL, "OpenAI-compatible API base URL")
	fs.StringVar(&cfg.Model, "model", cfg.Model, "model name")
	fs.StringVar(&cfg.APIKey, "api-key", cfg.APIKey, "optional API key")
	fs.StringVar(&cfg.WorkDir, "workdir", cfg.WorkDir, "workspace root")
	fs.StringVar(&cfg.StateDir, "state-dir", cfg.StateDir, "state directory for sessions and transcripts")
	fs.IntVar(&cfg.MaxSteps, "max-steps", cfg.MaxSteps, "maximum agent steps")
	fs.StringVar(&cfg.Shell, "shell", cfg.Shell, "shell executable")
	fs.StringVar(&cfg.ApprovalMode, "approval", cfg.ApprovalMode, "approval mode: confirm or dangerously")
	fs.BoolVar(&dangerously, "dangerously", false, "shortcut for --approval dangerously")
	fs.StringVar(&opts.outputMode, "output", opts.outputMode, "output mode: raw or terminal")
	fs.StringVar(&opts.session, "session", opts.session, "session name for chat persistence")

	timeoutText := cfg.CommandTimeout.String()
	fs.StringVar(&timeoutText, "command-timeout", timeoutText, "shell command timeout")

	if err := fs.Parse(args); err != nil {
		return cfg, runtimeOptions{}, nil, nil, err
	}
	if err := cfg.SetCommandTimeout(timeoutText); err != nil {
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

	opts.outputMode = strings.ToLower(strings.TrimSpace(opts.outputMode))
	if opts.outputMode != "raw" && opts.outputMode != "terminal" {
		return cfg, runtimeOptions{}, nil, nil, fmt.Errorf("invalid output mode %q", opts.outputMode)
	}

	return cfg, opts, fs.Args(), bufio.NewReader(os.Stdin), nil
}

func buildAgent(cfg config.Config, reader *bufio.Reader) (*agent.Agent, *tools.TerminalApprover) {
	interactive := tools.IsInteractiveTerminal(os.Stdin)
	approver := tools.NewTerminalApprover(reader, os.Stderr, cfg.ApprovalMode, interactive)
	client := openaiapi.NewClient(cfg.BaseURL, cfg.Model, cfg.APIKey)
	registry := tools.NewRegistry(cfg.WorkDir, cfg.Shell, cfg.CommandTimeout, approver)
	return agent.New(client, registry, cfg.MaxSteps, os.Stderr), approver
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
	fmt.Fprintln(os.Stderr, "  onek run [flags] <task>")
	fmt.Fprintln(os.Stderr, "  onek chat [flags]")
	fmt.Fprintln(os.Stderr, "  onek ping [flags]")
	fmt.Fprintln(os.Stderr, "  onek models [flags]")
	fmt.Fprintln(os.Stderr, "  onek version")
	fmt.Fprintln(os.Stderr)
	printRunUsage()
}

func printRunUsage() {
	fmt.Fprintln(os.Stderr, "Examples:")
	fmt.Fprintln(os.Stderr, `  onek run --approval confirm "inspect this repo"`)
	fmt.Fprintln(os.Stderr, `  onek run --dangerously "run go test ./..."`)
	fmt.Fprintln(os.Stderr, `  onek chat --session default --output terminal`)
}
