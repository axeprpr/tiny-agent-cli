package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tiny-agent-cli/internal/config"
	"tiny-agent-cli/internal/harness"
	"tiny-agent-cli/internal/memory"
	"tiny-agent-cli/internal/session"
	"tiny-agent-cli/internal/tools"
)

func parseWorkspaceFlags(name string, args []string) (config.Config, []string, error) {
	cfg := config.FromEnv()
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.WorkDir, "workdir", cfg.WorkDir, "workspace root")
	if err := fs.Parse(args); err != nil {
		return cfg, nil, err
	}
	absWorkDir, err := filepath.Abs(cfg.WorkDir)
	if err != nil {
		return cfg, nil, fmt.Errorf("resolve workdir: %w", err)
	}
	cfg.WorkDir = absWorkDir
	if strings.TrimSpace(os.Getenv("AGENT_STATE_DIR")) == "" {
		cfg.StateDir = config.DefaultStateDir(cfg.WorkDir)
	}
	absStateDir, err := filepath.Abs(cfg.StateDir)
	if err != nil {
		return cfg, nil, fmt.Errorf("resolve state dir: %w", err)
	}
	cfg.StateDir = absStateDir
	cfg.ApprovalMode = tools.NormalizePermissionMode(cfg.ApprovalMode)
	return cfg, fs.Args(), nil
}

func runPlan(args []string) int {
	cfg, extra, err := parseWorkspaceFlags("plan", args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid plan config: %v\n", err)
		printPlanUsage()
		return 2
	}
	if len(extra) != 0 {
		fmt.Fprintf(os.Stderr, "invalid plan config: unexpected arguments: %s\n", strings.Join(extra, " "))
		printPlanUsage()
		return 2
	}
	_, text, err := readPlanFile(cfg.WorkDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plan error: %v\n", err)
		return 1
	}
	fmt.Println(text)
	return 0
}

func runStatus(args []string) int {
	cfg, extra, err := parseWorkspaceFlags("status", args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid status config: %v\n", err)
		printStatusUsage()
		return 2
	}
	if len(extra) != 0 {
		fmt.Fprintf(os.Stderr, "invalid status config: unexpected arguments: %s\n", strings.Join(extra, " "))
		printStatusUsage()
		return 2
	}
	fmt.Println(renderWorkspaceStatus(cfg))
	return 0
}

func runSkills(args []string) int {
	cfg, extra, err := parseWorkspaceFlags("skills", args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid skills config: %v\n", err)
		printSkillsUsage()
		return 2
	}
	if len(extra) != 0 {
		fmt.Fprintf(os.Stderr, "invalid skills config: unexpected arguments: %s\n", strings.Join(extra, " "))
		printSkillsUsage()
		return 2
	}
	skills, err := tools.DiscoverSkills(cfg.WorkDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skills error: %v\n", err)
		return 1
	}
	fmt.Println(formatSkills(skills))
	return 0
}

func readPlanFile(workDir string) (string, string, error) {
	paths := []string{
		filepath.Join(workDir, "plan.md"),
		filepath.Join(workDir, "docs", "plan.md"),
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err == nil {
			return path, strings.TrimSpace(string(data)), nil
		}
		if !os.IsNotExist(err) {
			return "", "", err
		}
	}
	return "", "", os.ErrNotExist
}

func renderWorkspaceStatus(cfg config.Config) string {
	ctx := harness.BuildPromptContext(cfg, nil, "", "")
	planPath, _, planErr := readPlanFile(cfg.WorkDir)
	summaries, _ := session.ListSessions(cfg.StateDir)
	memPath := memory.Path(cfg.StateDir)
	permissionPath := tools.PermissionPath(cfg.StateDir)
	auditPath := tools.AuditPath(cfg.StateDir)
	commandRules := 0
	if store, err := tools.LoadPermissionStore(permissionPath); err == nil && store != nil {
		commandRules = len(store.CommandRules())
	}

	lines := []string{
		"workdir=" + cfg.WorkDir,
		"state=" + cfg.StateDir,
		"approval=" + cfg.ApprovalMode,
	}
	if planErr == nil {
		lines = append(lines, "plan="+planPath)
	} else {
		lines = append(lines, "plan=(missing)")
	}
	if strings.TrimSpace(ctx.GitBranch) != "" {
		lines = append(lines, "git_branch="+ctx.GitBranch)
	}
	if strings.TrimSpace(ctx.GitStatus) != "" {
		lines = append(lines, "git_status="+compactJobText(ctx.GitStatus, 180))
	}
	lines = append(lines,
		fmt.Sprintf("instructions=%d", len(ctx.Instructions)),
		fmt.Sprintf("skills=%d", len(ctx.Skills)),
		fmt.Sprintf("sessions=%d", len(summaries)),
		fmt.Sprintf("command_rules=%d", commandRules),
		"memory="+presenceStatus(memPath),
		"policy="+presenceStatus(permissionPath),
		"audit="+presenceStatus(auditPath),
	)
	return strings.Join(lines, "\n")
}

func formatSkills(skills []tools.Skill) string {
	if len(skills) == 0 {
		return "no skills discovered"
	}
	lines := make([]string, 0, len(skills))
	for _, skill := range skills {
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

func presenceStatus(path string) string {
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return "(missing)"
}

func printPlanUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  tacli plan [--workdir <path>]")
}

func printStatusUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  tacli status [--workdir <path>]")
}

func printSkillsUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  tacli skills [--workdir <path>]")
}
