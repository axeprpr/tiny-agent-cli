package harness

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

const (
	maxInstructionFileChars  = 4000
	maxTotalInstructionChars = 12000
)

func NewFactory(cfg config.Config) Factory {
	return Factory{cfg: cfg}
}

func (f Factory) NewApprover(reader *bufio.Reader, output io.Writer, interactive bool) tools.Approver {
	return tools.NewTerminalApprover(reader, output, f.cfg.ApprovalMode, interactive)
}

func (f Factory) NewAgent(approver tools.Approver, log io.Writer, jobs tools.JobControl, policy *tools.PermissionStore, extraAuditSinks ...tools.ToolAuditSink) *agent.Agent {
	client := f.NewModelClient()
	registry := tools.NewRegistryWithOptions(f.cfg.WorkDir, f.cfg.Shell, f.cfg.CommandTimeout, nil, f.cfg.Hooks, nil, jobs)
	permissionPolicy := tools.NewApprovalPermissionPolicy(f.cfg.WorkDir, approver, policy)
	fileSink := tools.NewFileAuditSink(tools.AuditPath(f.cfg.StateDir))
	allSinks := append([]tools.ToolAuditSink{fileSink}, extraAuditSinks...)
	registry.SetAuditSink(tools.NewFanoutAuditSink(allSinks...))

	a := agent.New(client, registry, f.cfg.ContextWindow, log)
	a.SetStreamClient(client)
	a.SetToolPermissionPolicy(permissionPolicy)
	a.SetToolHookRunner(tools.NewHookRunner(f.cfg.Hooks))
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
	instructions, _ := discoverInstructionFiles(cfg.WorkDir)
	gitBranch, gitStatus := gitPromptContext(cfg.WorkDir)
	return agent.PromptContext{
		MemoryText:   memoryText,
		WorkDir:      cfg.WorkDir,
		Shell:        cfg.Shell,
		ApprovalMode: cfg.ApprovalMode,
		Model:        cfg.Model,
		ToolNames:    toolNames,
		Skills:       skills,
		Instructions: instructions,
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

	statusOut, err := runGitPromptCmd(workDir, "status", "--short", "--branch")
	if err != nil {
		return branch, ""
	}
	statusOut = strings.TrimSpace(statusOut)
	if statusOut == "" {
		return branch, ""
	}
	lines := strings.Split(statusOut, "\n")
	if len(lines) == 1 && strings.HasPrefix(strings.TrimSpace(lines[0]), "## ") {
		return branch, "clean"
	}
	if len(lines) > 12 {
		lines = append(lines[:12], fmt.Sprintf("... (%d more lines)", len(lines)-12))
	}
	return branch, strings.Join(lines, "\n")
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

func discoverInstructionFiles(workDir string) ([]agent.PromptInstructionFile, error) {
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return nil, err
	}

	var dirs []string
	for dir := absWorkDir; ; dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	for i, j := 0, len(dirs)-1; i < j; i, j = i+1, j-1 {
		dirs[i], dirs[j] = dirs[j], dirs[i]
	}

	candidates := []string{
		"CLAW.md",
		"CLAW.local.md",
		filepath.Join(".claw", "CLAW.md"),
		filepath.Join(".claw", "instructions.md"),
	}

	var files []agent.PromptInstructionFile
	totalChars := 0
	seen := make(map[string]struct{})
	for _, dir := range dirs {
		for _, candidate := range candidates {
			path := filepath.Join(dir, candidate)
			if _, ok := seen[path]; ok {
				continue
			}
			item, ok, err := readInstructionFile(path, maxInstructionFileChars)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			remaining := maxTotalInstructionChars - totalChars
			if remaining <= 0 {
				return files, nil
			}
			if len(item.Content) > remaining {
				item.Content = item.Content[:remaining] + "\n...[truncated]"
			}
			seen[path] = struct{}{}
			totalChars += len(item.Content)
			files = append(files, item)
		}
	}
	return files, nil
}

func readInstructionFile(path string, maxChars int) (agent.PromptInstructionFile, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return agent.PromptInstructionFile{}, false, nil
		}
		return agent.PromptInstructionFile{}, false, err
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return agent.PromptInstructionFile{}, false, nil
	}
	if maxChars > 0 && len(content) > maxChars {
		content = content[:maxChars] + "\n...[truncated]"
	}
	return agent.PromptInstructionFile{
		Path:    path,
		Content: content,
	}, true, nil
}
