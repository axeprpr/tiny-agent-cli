package main

import (
	"io"

	"tiny-agent-cli/internal/agent"
	"tiny-agent-cli/internal/config"
	"tiny-agent-cli/internal/harness"
	"tiny-agent-cli/internal/model"
	"tiny-agent-cli/internal/tools"
)

// runtimeKernel isolates agent/session assembly from the CLI shell.
// It acts as the local "runtime core" boundary for chat flows.
type runtimeKernel struct {
	cfg        config.Config
	approver   tools.Approver
	jobs       tools.JobControl
	policy     *tools.PermissionStore
	log        io.Writer
	auditSinks []tools.ToolAuditSink
}

func newRuntimeKernel(cfg config.Config, approver tools.Approver, log io.Writer, jobs tools.JobControl, policy *tools.PermissionStore, auditSinks ...tools.ToolAuditSink) runtimeKernel {
	return runtimeKernel{
		cfg:        cfg,
		approver:   approver,
		jobs:       jobs,
		policy:     policy,
		log:        log,
		auditSinks: append([]tools.ToolAuditSink(nil), auditSinks...),
	}
}

func (k runtimeKernel) buildAgent() *agent.Agent {
	return harness.NewFactory(k.cfg).NewAgent(k.approver, k.log, k.jobs, k.policy, k.auditSinks...)
}

func (k runtimeKernel) promptContext(loop *agent.Agent, sessionMode string, provider runtimeContextProvider) agent.PromptContext {
	memoryText := ""
	if provider != nil {
		memoryText = provider.SystemMemory()
	}
	return harness.BuildPromptContext(k.cfg, loop, sessionMode, memoryText)
}

func (k runtimeKernel) newSession(loop *agent.Agent, sessionMode string, provider runtimeContextProvider) *agent.Session {
	return loop.NewSessionWithPrompt(k.promptContext(loop, sessionMode, provider))
}

func (k runtimeKernel) refreshSessionPrompt(session *agent.Session, loop *agent.Agent, sessionMode string, provider runtimeContextProvider) *agent.Session {
	if session == nil {
		return k.newSession(loop, sessionMode, provider)
	}
	messages := session.Messages()
	if len(messages) == 0 {
		return k.newSession(loop, sessionMode, provider)
	}
	messages[0] = model.Message{
		Role:    "system",
		Content: agent.BuildSystemPrompt(k.promptContext(loop, sessionMode, provider)),
	}
	session.ReplaceMessages(messages)
	return session
}
