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

func runContract(args []string) int {
	cfg, extra, err := parseWorkspaceFlags("contract", args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid contract config: %v\n", err)
		printContractUsage()
		return 2
	}
	if len(extra) != 0 {
		fmt.Fprintf(os.Stderr, "invalid contract config: unexpected arguments: %s\n", strings.Join(extra, " "))
		printContractUsage()
		return 2
	}
	path, text, err := readTaskContractFile(cfg.WorkDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "contract error: %v\n", err)
		return 1
	}
	if strings.TrimSpace(text) == "" {
		fmt.Println("(no task contract)")
		return 0
	}
	fmt.Println("path=" + path)
	fmt.Println(text)
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

func runCapabilities(args []string) int {
	_, extra, err := parseWorkspaceFlags("capabilities", args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid capabilities config: %v\n", err)
		printCapabilitiesUsage()
		return 2
	}
	switch len(extra) {
	case 0:
		fmt.Println(formatCapabilities(tools.BundledCapabilityPacks()))
		return 0
	case 1:
		pack, ok := tools.FindCapabilityPack(extra[0])
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown capability: %s\n", extra[0])
			return 1
		}
		fmt.Println(formatCapabilities([]tools.CapabilityPack{pack}))
		return 0
	default:
		fmt.Fprintf(os.Stderr, "invalid capabilities config: unexpected arguments: %s\n", strings.Join(extra, " "))
		printCapabilitiesUsage()
		return 2
	}
}

func readPlanFile(workDir string) (string, string, error) {
	path := filepath.Join(workDir, "plan.md")
	data, err := os.ReadFile(path)
	if err == nil {
		return path, strings.TrimSpace(string(data)), nil
	}
	if os.IsNotExist(err) {
		return "", "", os.ErrNotExist
	}
	return "", "", err
}

func readTaskContractFile(workDir string) (string, string, error) {
	path := tools.ContractPath(workDir)
	contract, err := tools.LoadTaskContract(path)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(contract.Objective) == "" && len(contract.Deliverables) == 0 && len(contract.AcceptanceChecks) == 0 {
		return path, "", nil
	}
	return path, tools.FormatTaskContract(contract), nil
}

func renderWorkspaceStatus(cfg config.Config) string {
	ctx := harness.BuildPromptContext(cfg, nil, "", "")
	planPath, _, planErr := readPlanFile(cfg.WorkDir)
	contractPath, contractText, contractErr := readTaskContractFile(cfg.WorkDir)
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
	if contractErr == nil && strings.TrimSpace(contractText) != "" {
		lines = append(lines, "contract="+contractPath)
		lines = append(lines, contractStatusLine(cfg.WorkDir))
	} else {
		lines = append(lines, "contract=(missing)")
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
		fmt.Sprintf("capabilities=%d", len(ctx.Capabilities)),
		fmt.Sprintf("sessions=%d", len(summaries)),
		fmt.Sprintf("command_rules=%d", commandRules),
		"memory="+presenceStatus(memPath),
		"policy="+presenceStatus(permissionPath),
		"audit="+presenceStatus(auditPath),
	)
	return strings.Join(lines, "\n")
}

func contractStatusLine(workDir string) string {
	contract, err := tools.LoadTaskContract(tools.ContractPath(workDir))
	if err != nil || strings.TrimSpace(contract.Objective) == "" {
		return "contract_summary=(none)"
	}
	completedChecks := 0
	evidencedChecks := 0
	for _, item := range contract.AcceptanceChecks {
		if item.Status == "completed" {
			completedChecks++
		}
		if strings.TrimSpace(item.Evidence) != "" {
			evidencedChecks++
		}
	}
	return fmt.Sprintf("contract_kind=%s checks=%d/%d evidenced=%d/%d",
		firstNonEmpty(strings.TrimSpace(contract.TaskKind), "(unset)"),
		completedChecks, len(contract.AcceptanceChecks),
		evidencedChecks, len(contract.AcceptanceChecks),
	)
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

func formatCapabilities(packs []tools.CapabilityPack) string {
	if len(packs) == 0 {
		return "no capability packs available"
	}
	blocks := make([]string, 0, len(packs))
	for _, pack := range packs {
		var lines []string
		lines = append(lines, pack.Name+": "+pack.Description)
		if strings.TrimSpace(pack.When) != "" {
			lines = append(lines, "when: "+pack.When)
		}
		if len(pack.Roles) > 0 {
			lines = append(lines, "roles: "+strings.Join(pack.Roles, ", "))
		}
		if len(pack.Tools) > 0 {
			lines = append(lines, "tools: "+strings.Join(pack.Tools, ", "))
		}
		if len(pack.Steps) > 0 {
			lines = append(lines, "steps:")
			for _, step := range pack.Steps {
				if strings.TrimSpace(step) != "" {
					lines = append(lines, "- "+strings.TrimSpace(step))
				}
			}
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	return strings.Join(blocks, "\n\n")
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

func printContractUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  tacli contract [--workdir <path>]")
}

func printSkillsUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  tacli skills [--workdir <path>]")
}

func printCapabilitiesUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  tacli capabilities [--workdir <path>] [name]")
}
