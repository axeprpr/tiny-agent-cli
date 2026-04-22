package main

import (
	"encoding/json"
	"errors"

	"tiny-agent-cli/internal/agent"
	"tiny-agent-cli/internal/config"
	"tiny-agent-cli/internal/tools"
)

// runtimeOrchestrator isolates background job orchestration from the chat shell.
// chatRuntime should depend on this boundary instead of jobManager internals.
type runtimeOrchestrator struct {
	manager *jobManager
}

func newRuntimeOrchestrator(cfg config.Config, memoryText string) *runtimeOrchestrator {
	return &runtimeOrchestrator{manager: newJobManager(cfg, memoryText)}
}

func (o *runtimeOrchestrator) SetNotifier(fn func(string)) {
	if o == nil || o.manager == nil {
		return
	}
	o.manager.SetNotifier(fn)
}

func (o *runtimeOrchestrator) SetRoleRouter(router backgroundRoleRouter) {
	if o == nil || o.manager == nil {
		return
	}
	o.manager.SetRoleRouter(router)
}

func (o *runtimeOrchestrator) UpdateConfig(cfg config.Config) {
	if o == nil || o.manager == nil {
		return
	}
	o.manager.UpdateConfig(cfg)
}

func (o *runtimeOrchestrator) UpdateMemory(memoryText string) {
	if o == nil || o.manager == nil {
		return
	}
	o.manager.UpdateMemory(memoryText)
}

func (o *runtimeOrchestrator) Start(task string) (string, error) {
	if o == nil || o.manager == nil {
		return "", errBackgroundOrchestratorUnavailable()
	}
	return o.manager.Start(task)
}

func (o *runtimeOrchestrator) StartWithRole(role, task string) (string, error) {
	if o == nil || o.manager == nil {
		return "", errBackgroundOrchestratorUnavailable()
	}
	return o.manager.StartWithRole(role, task)
}

func (o *runtimeOrchestrator) StartWithRoleAndOptions(role, task string, opts tools.BackgroundStartOptions) (string, error) {
	if o == nil || o.manager == nil {
		return "", errBackgroundOrchestratorUnavailable()
	}
	return o.manager.StartWithRoleAndOptions(role, task, opts)
}

func (o *runtimeOrchestrator) Send(id, task string) error {
	if o == nil || o.manager == nil {
		return errBackgroundOrchestratorUnavailable()
	}
	return o.manager.Send(id, task)
}

func (o *runtimeOrchestrator) Cancel(id string) error {
	if o == nil || o.manager == nil {
		return errBackgroundOrchestratorUnavailable()
	}
	return o.manager.Cancel(id)
}

func (o *runtimeOrchestrator) List() []tools.BackgroundJobSnapshot {
	if o == nil || o.manager == nil {
		return nil
	}
	return o.manager.ToolList()
}

func (o *runtimeOrchestrator) Snapshot(id string) (tools.BackgroundJobSnapshot, bool) {
	if o == nil || o.manager == nil {
		return tools.BackgroundJobSnapshot{}, false
	}
	return o.manager.ToolSnapshot(id)
}

func (o *runtimeOrchestrator) ListJobs() []jobSnapshot {
	if o == nil || o.manager == nil {
		return nil
	}
	return o.manager.List()
}

func (o *runtimeOrchestrator) JobSnapshot(id string) (jobSnapshot, bool) {
	if o == nil || o.manager == nil {
		return jobSnapshot{}, false
	}
	return o.manager.Snapshot(id)
}

func (o *runtimeOrchestrator) CollectReadyForApply() []jobSnapshot {
	if o == nil || o.manager == nil {
		return nil
	}
	return o.manager.CollectReadyForApply()
}

func (o *runtimeOrchestrator) MarkApplied(id string) {
	if o == nil || o.manager == nil {
		return
	}
	o.manager.MarkApplied(id)
}

func (o *runtimeOrchestrator) Summary() string {
	if o == nil || o.manager == nil {
		return "jobs=0"
	}
	return o.manager.Summary()
}

func (o *runtimeOrchestrator) Export() json.RawMessage {
	if o == nil || o.manager == nil {
		return nil
	}
	return o.manager.Export()
}

func (o *runtimeOrchestrator) Restore(raw json.RawMessage) error {
	if o == nil || o.manager == nil {
		return nil
	}
	return o.manager.Restore(raw)
}

func (o *runtimeOrchestrator) ClearFinished() int {
	if o == nil || o.manager == nil {
		return 0
	}
	return o.manager.ClearFinished()
}

func (o *runtimeOrchestrator) ListSubagents() []agent.SubagentSnapshot {
	if o == nil || o.manager == nil || o.manager.orchestration == nil {
		return nil
	}
	return o.manager.orchestration.List()
}

func (o *runtimeOrchestrator) SubagentSnapshot(id string) (agent.SubagentSnapshot, bool) {
	if o == nil || o.manager == nil || o.manager.orchestration == nil {
		return agent.SubagentSnapshot{}, false
	}
	return o.manager.orchestration.Snapshot(id)
}

func errBackgroundOrchestratorUnavailable() error {
	return errors.New("background orchestrator unavailable")
}
