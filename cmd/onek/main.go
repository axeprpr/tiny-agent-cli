package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"onek-agent/internal/agent"
	"onek-agent/internal/config"
	"onek-agent/internal/model/openaiapi"
	"onek-agent/internal/tools"
)

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
	cfg := config.FromEnv()

	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.BaseURL, "base-url", cfg.BaseURL, "OpenAI-compatible API base URL")
	fs.StringVar(&cfg.Model, "model", cfg.Model, "model name")
	fs.StringVar(&cfg.APIKey, "api-key", cfg.APIKey, "optional API key")
	fs.StringVar(&cfg.WorkDir, "workdir", cfg.WorkDir, "workspace root")
	fs.IntVar(&cfg.MaxSteps, "max-steps", cfg.MaxSteps, "maximum agent steps")
	fs.StringVar(&cfg.Shell, "shell", cfg.Shell, "shell executable")

	timeoutText := cfg.CommandTimeout.String()
	fs.StringVar(&timeoutText, "command-timeout", timeoutText, "shell command timeout")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := cfg.SetCommandTimeout(timeoutText); err != nil {
		fmt.Fprintf(os.Stderr, "invalid command timeout: %v\n", err)
		return 2
	}

	task := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if task == "" {
		fmt.Fprintln(os.Stderr, "missing task")
		printRunUsage()
		return 2
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "invalid config: %v\n", err)
		return 2
	}

	client := openaiapi.NewClient(cfg.BaseURL, cfg.Model, cfg.APIKey)
	registry := tools.NewRegistry(cfg.WorkDir, cfg.Shell, cfg.CommandTimeout)
	loop := agent.New(client, registry, cfg.MaxSteps, os.Stderr)

	result, err := loop.Run(context.Background(), task)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
		return 1
	}

	fmt.Println(result.Final)
	return 0
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  onek run [flags] <task>")
	fmt.Fprintln(os.Stderr)
	printRunUsage()
}

func printRunUsage() {
	fmt.Fprintln(os.Stderr, "Example:")
	fmt.Fprintln(os.Stderr, `  onek run "list the largest files in this repo"`)
}
