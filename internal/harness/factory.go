package harness

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"tiny-agent-cli/internal/agent"
	"tiny-agent-cli/internal/config"
	"tiny-agent-cli/internal/model/openaiapi"
	"tiny-agent-cli/internal/tools"
)

// Factory wires runtime dependencies for the interactive and batch agent flows.
type Factory struct {
	cfg config.Config
}

func NewFactory(cfg config.Config) Factory {
	return Factory{cfg: cfg}
}

func (f Factory) NewApprover(reader *bufio.Reader, output io.Writer, interactive bool) tools.Approver {
	return tools.NewTerminalApprover(reader, output, f.cfg.ApprovalMode, interactive)
}

func (f Factory) NewAgent(approver tools.Approver, log io.Writer, jobs tools.JobControl, policy *tools.PermissionStore, extraAuditSinks ...tools.ToolAuditSink) *agent.Agent {
	client := f.NewModelClient()
	registry := tools.NewRegistryWithOptions(f.cfg.WorkDir, f.cfg.Shell, f.cfg.CommandTimeout, approver, f.cfg.Hooks, policy, jobs)
	fileSink := tools.NewFileAuditSink(tools.AuditPath(f.cfg.StateDir))
	allSinks := append([]tools.ToolAuditSink{fileSink}, extraAuditSinks...)
	registry.SetAuditSink(tools.NewFanoutAuditSink(allSinks...))

	a := agent.New(client, registry, f.cfg.ContextWindow, log)
	a.SetStreamClient(client)
	return a
}

func (f Factory) NewModelClient() *openaiapi.Client {
	return openaiapi.NewClient(f.cfg.BaseURL, f.cfg.Model, f.cfg.APIKey, openaiapi.WithTimeout(f.cfg.ModelTimeout))
}

func BuildPromptContext(cfg config.Config, loop *agent.Agent, sessionMode, memoryText string) agent.PromptContext {
	var toolNames []string
	if loop != nil {
		toolNames = loop.ToolNames()
	}
	var skills []agent.PromptSkill
	if discovered, err := tools.DiscoverSkills(cfg.WorkDir); err == nil {
		skills = make([]agent.PromptSkill, 0, len(discovered))
		for _, item := range discovered {
			skills = append(skills, agent.PromptSkill{
				Name:        item.Name,
				Description: item.Description,
				Path:        item.Path,
			})
		}
	}
	gitBranch, gitStatus := gitPromptContext(cfg.WorkDir)
	return agent.PromptContext{
		MemoryText:   memoryText,
		WorkDir:      cfg.WorkDir,
		Shell:        cfg.Shell,
		ApprovalMode: cfg.ApprovalMode,
		Model:        cfg.Model,
		ToolNames:    toolNames,
		Skills:       skills,
		GitBranch:    gitBranch,
		GitStatus:    gitStatus,
		SessionMode:  sessionMode,
		AgentRole:    roleFromSessionMode(sessionMode),
	}
}

func gitPromptContext(workDir string) (branch string, status string) {
	branchOut, err := runGitPromptCmd(workDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", ""
	}
	branch = strings.TrimSpace(branchOut)
	if branch == "" || branch == "HEAD" {
		branch = strings.TrimSpace(firstLine(branchOut))
	}

	statusOut, err := runGitPromptCmd(workDir, "status", "--porcelain")
	if err != nil {
		return branch, ""
	}
	lines := strings.Split(strings.TrimSpace(statusOut), "\n")
	changes := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			changes++
		}
	}
	if changes == 0 {
		return branch, "clean"
	}
	return branch, "dirty(" + strconv.Itoa(changes) + " changes)"
}

func runGitPromptCmd(workDir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", workDir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func firstLine(text string) string {
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		return text[:idx]
	}
	return text
}

func roleFromSessionMode(sessionMode string) string {
	trimmed := strings.TrimSpace(sessionMode)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	const prefix = "background:"
	if !strings.HasPrefix(lower, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(lower, prefix))
}
