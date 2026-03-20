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
	"onek-agent/internal/model/openaiapi"
	"onek-agent/internal/tools"
)

var version = "dev"

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
	cfg, outputMode, taskArgs, reader, err := parseAgentFlags("run", args)
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

	loop := buildAgent(cfg, reader)
	result, err := loop.Run(context.Background(), task)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
		return 1
	}

	fmt.Println(formatRunOutput(result.Final, outputMode))
	return 0
}

func runChat(args []string) int {
	cfg, outputMode, _, reader, err := parseAgentFlags("chat", args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid config: %v\n", err)
		return 2
	}

	loop := buildAgent(cfg, reader)
	session := loop.NewSession()
	interactive := tools.IsInteractiveTerminal(os.Stdin)

	if interactive {
		fmt.Fprintln(os.Stderr, "Interactive mode. Type /exit to quit, /reset to clear context.")
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
		switch task {
		case "":
			if readErr != nil {
				return 0
			}
			continue
		case "/exit", "/quit":
			return 0
		case "/reset":
			session = loop.NewSession()
			fmt.Fprintln(os.Stderr, "context reset")
			if readErr != nil {
				return 0
			}
			continue
		case "/help":
			fmt.Fprintln(os.Stderr, "/exit  quit")
			fmt.Fprintln(os.Stderr, "/reset clear conversation context")
			fmt.Fprintln(os.Stderr, "/help  show commands")
			if readErr != nil {
				return 0
			}
			continue
		}

		result, err := session.RunTask(context.Background(), task)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
		} else {
			fmt.Println(formatRunOutput(result.Final, outputMode))
		}

		if readErr != nil {
			return 0
		}
	}
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

func parseAgentFlags(name string, args []string) (config.Config, string, []string, *bufio.Reader, error) {
	cfg := config.FromEnv()
	outputMode := "raw"
	dangerously := false

	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.BaseURL, "base-url", cfg.BaseURL, "OpenAI-compatible API base URL")
	fs.StringVar(&cfg.Model, "model", cfg.Model, "model name")
	fs.StringVar(&cfg.APIKey, "api-key", cfg.APIKey, "optional API key")
	fs.StringVar(&cfg.WorkDir, "workdir", cfg.WorkDir, "workspace root")
	fs.IntVar(&cfg.MaxSteps, "max-steps", cfg.MaxSteps, "maximum agent steps")
	fs.StringVar(&cfg.Shell, "shell", cfg.Shell, "shell executable")
	fs.StringVar(&cfg.ApprovalMode, "approval", cfg.ApprovalMode, "approval mode: confirm or dangerously")
	fs.BoolVar(&dangerously, "dangerously", false, "shortcut for --approval dangerously")
	fs.StringVar(&outputMode, "output", outputMode, "output mode: raw or terminal")

	timeoutText := cfg.CommandTimeout.String()
	fs.StringVar(&timeoutText, "command-timeout", timeoutText, "shell command timeout")

	if err := fs.Parse(args); err != nil {
		return cfg, "", nil, nil, err
	}
	if err := cfg.SetCommandTimeout(timeoutText); err != nil {
		return cfg, "", nil, nil, err
	}
	if dangerously {
		cfg.ApprovalMode = tools.ApprovalDangerously
	}
	if err := cfg.Validate(); err != nil {
		return cfg, "", nil, nil, err
	}

	cfg.ApprovalMode = strings.ToLower(strings.TrimSpace(cfg.ApprovalMode))
	switch cfg.ApprovalMode {
	case "", tools.ApprovalConfirm:
		cfg.ApprovalMode = tools.ApprovalConfirm
	case tools.ApprovalDangerously:
	default:
		return cfg, "", nil, nil, fmt.Errorf("invalid approval mode %q", cfg.ApprovalMode)
	}

	outputMode = strings.ToLower(strings.TrimSpace(outputMode))
	if outputMode != "raw" && outputMode != "terminal" {
		return cfg, "", nil, nil, fmt.Errorf("invalid output mode %q", outputMode)
	}

	return cfg, outputMode, fs.Args(), bufio.NewReader(os.Stdin), nil
}

func buildAgent(cfg config.Config, reader *bufio.Reader) *agent.Agent {
	interactive := tools.IsInteractiveTerminal(os.Stdin)
	approver := tools.NewTerminalApprover(reader, os.Stderr, cfg.ApprovalMode, interactive)
	client := openaiapi.NewClient(cfg.BaseURL, cfg.Model, cfg.APIKey)
	registry := tools.NewRegistry(cfg.WorkDir, cfg.Shell, cfg.CommandTimeout, approver)
	return agent.New(client, registry, cfg.MaxSteps, os.Stderr)
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
	fmt.Fprintln(os.Stderr, `  onek chat --output terminal`)
}
