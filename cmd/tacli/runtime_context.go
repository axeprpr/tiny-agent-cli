package main

import "tiny-agent-cli/internal/memory"

// runtimeContextProvider supplies the runtime prompt context that gets injected
// into the agent kernel. Keeping this separate makes memory/prompt policy
// swappable without teaching chatRuntime how to rebuild system prompts.
type runtimeContextProvider interface {
	SystemMemory() string
}

type memoryContextProvider struct {
	global  []string
	team    []string
	project []string
}

func newMemoryContextProvider(global, team, project []string) memoryContextProvider {
	return memoryContextProvider{
		global:  append([]string(nil), global...),
		team:    append([]string(nil), team...),
		project: append([]string(nil), project...),
	}
}

func (p memoryContextProvider) SystemMemory() string {
	return memory.RenderSystemMemory(p.global, p.team, p.project)
}
