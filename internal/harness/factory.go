package harness

import (
	"bufio"
	"io"
	"strings"

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

func (f Factory) NewAgent(approver tools.Approver, log io.Writer, jobs tools.JobControl, extraAuditSinks ...tools.ToolAuditSink) *agent.Agent {
	client := f.NewModelClient()
	registry := tools.NewRegistry(f.cfg.WorkDir, f.cfg.Shell, f.cfg.CommandTimeout, approver, jobs)
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
	return agent.PromptContext{
		MemoryText:   memoryText,
		WorkDir:      cfg.WorkDir,
		Shell:        cfg.Shell,
		ApprovalMode: cfg.ApprovalMode,
		Model:        cfg.Model,
		ToolNames:    toolNames,
		SessionMode:  sessionMode,
		AgentRole:    roleFromSessionMode(sessionMode),
	}
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
