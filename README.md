# tacli

`tacli` is a small terminal coding agent for one workspace, one binary, and one OpenAI-compatible model endpoint.

It keeps the runtime narrow:

- Go binary only
- local workspace state under `.tacli/`
- built-in tool runtime with permissions, hooks, audit, and background jobs
- interactive chat plus one-shot task execution

Chinese version: [README.zh-CN.md](README.zh-CN.md)

## Architecture

### 1. System Overview

```mermaid
flowchart LR
    user[User]

    subgraph entry[Entrypoints]
        cli["cmd/tacli/main.go<br/>run / chat / status / init / models / version"]
        tui["cmd/tacli/chat_tui.go<br/>interactive terminal UI"]
        control["cmd/tacli/control.go<br/>plan / status / contract / skills / capabilities"]
    end

    subgraph runtime[Runtime Assembly]
        chatrt["chatRuntime<br/>session paths, memory scopes, jobs, plugins"]
        factory["internal/harness/factory.go<br/>build prompt context and wire dependencies"]
        prompt["PromptContext<br/>instructions + skills + capabilities + git + memory"]
    end

    subgraph core[Agent Core]
        agent["internal/agent<br/>session loop, retries, compaction, finish gate"]
        model["internal/model/openaiapi<br/>OpenAI-compatible HTTP + streaming client"]
        registry["internal/tools/registry.go<br/>tool definitions and execution pipeline"]
    end

    subgraph tooling[Tool Runtime]
        permission["permission policy<br/>tool policy + command rules"]
        hooks["hook runner<br/>pre/post tool hooks"]
        audit["audit sink"]
        tools["built-in tools<br/>todo / contract / files / edit / shell / web / MCP / bg jobs"]
    end

    subgraph state[Persistence]
        session["internal/session<br/>saved chats and transcripts"]
        memory["internal/memory"]
        tasks["internal/tasks + task contract + todo"]
        trace["trace + audit logs"]
        disk["workspace + .tacli/"]
    end

    user --> cli
    user --> tui
    cli --> control
    cli --> chatrt
    tui --> chatrt
    chatrt --> factory
    factory --> prompt
    factory --> agent
    agent --> model
    agent --> registry
    registry --> permission
    registry --> hooks
    registry --> audit
    registry --> tools
    chatrt --> session
    chatrt --> memory
    chatrt --> tasks
    chatrt --> trace
    session --> disk
    memory --> disk
    tasks --> disk
    trace --> disk
```

### 2. Turn Execution Flow

```mermaid
sequenceDiagram
    participant User
    participant UI as CLI / TUI
    participant Runtime as chatRuntime
    participant Factory as harness.Factory
    participant Session as agent.Session
    participant Model as model client
    participant Registry as tool registry
    participant Tool as concrete tool

    User->>UI: submit task
    UI->>Runtime: start task / chat turn
    Runtime->>Factory: build prompt context
    Factory->>Session: NewSessionWithPrompt(...)
    Session->>Model: chat/completions
    Model-->>Session: assistant response or tool calls

    alt assistant requests tools
        loop each tool call
            Session->>Registry: Call(name, args)
            Registry->>Registry: validate schema
            Registry->>Registry: apply permission policy
            Registry->>Registry: run pre-hooks
            Registry->>Tool: execute
            Tool-->>Registry: output / error
            Registry->>Registry: run post-hooks + audit
            Registry-->>Session: tool result message
        end
        Session->>Model: follow-up request with tool results
        Model-->>Session: next response
    end

    Session-->>Runtime: final answer + turn summary
    Runtime-->>UI: stream / render result
```

### 3. State and File Layout

```mermaid
flowchart TB
    subgraph repo[Workspace Root]
        claw["CLAW.md / .claw/<br/>repo instructions"]
        roadmap["plan.md<br/>used by tacli plan"]
        work["source tree"]
        state[".tacli/"]
    end

    subgraph files[.tacli Contents]
        sessions["sessions/*.json<br/>saved conversations"]
        transcripts["transcripts/*.log<br/>human-readable transcript log"]
        perms["permissions.json<br/>tool + command policy"]
        memory["memory.json<br/>global/team/project memory"]
        todo["tasks-v2.json<br/>todo list"]
        contract["contract-v1.json<br/>task contract"]
        audit["audit.jsonl<br/>tool events"]
        trace["trace.jsonl<br/>runtime events"]
    end

    subgraph live[Runtime Readers/Writers]
        promptctx["prompt builder"]
        sessionstore["session store"]
        toolruntime["tool runtime"]
        agentloop["agent loop"]
    end

    claw --> promptctx
    roadmap --> promptctx
    work --> toolruntime
    state --> sessions
    state --> transcripts
    state --> perms
    state --> memory
    state --> todo
    state --> contract
    state --> audit
    state --> trace

    sessionstore <--> sessions
    sessionstore <--> transcripts
    toolruntime <--> perms
    promptctx <--> memory
    agentloop <--> todo
    agentloop <--> contract
    toolruntime <--> audit
    agentloop <--> trace
```

## Repository Map

| Path | Responsibility |
| --- | --- |
| `cmd/tacli/` | CLI entrypoints, interactive chat runtime, TUI, slash-command parity, background job manager |
| `internal/harness/` | dependency wiring, prompt context construction, model/agent/tool assembly |
| `internal/agent/` | session loop, turn summaries, retries, compaction, finish gate, orchestration |
| `internal/tools/` | tool registry, permission layer, hooks, audit, task contract, file/shell/web/MCP tools |
| `internal/model/openaiapi/` | OpenAI-compatible HTTP client and streaming transport |
| `internal/session/` | saved sessions and transcript persistence |
| `internal/memory/` | persistent memory store |
| `internal/tasks/` | lightweight task records used by the CLI control plane |
| `release-site/` | static release page |
| `scripts/` | release, install, parity, and regression helpers |

## Usage

### 1. Build

```bash
go build -o tacli ./cmd/tacli
```

### 2. Configure a Model Endpoint

```bash
export MODEL_BASE_URL="https://api.openai.com/v1"
export MODEL_NAME="gpt-5-mini"
export MODEL_API_KEY="your-api-key"
```

Common runtime knobs:

- `AGENT_APPROVAL=confirm|dangerously`
- `AGENT_WORKDIR=/path/to/repo`
- `AGENT_STATE_DIR=/path/to/.tacli`
- `MODEL_CONTEXT_WINDOW=...`

### 3. Run It

```bash
tacli ping
tacli chat
```

One-shot task:

```bash
tacli run "inspect this repository and summarize the architecture"
```

Trusted local mode:

```bash
tacli chat --dangerously
tacli run --dangerously "go test ./..."
```

### 4. Useful Commands

```bash
tacli status
tacli models
tacli version
tacli plan
tacli contract
```

Inside chat:

```text
/status
/plan
/contract
/skills
/capabilities
/policy ...
/bg ...
```

## Notes

- `tacli` persists local state under `.tacli/` by default.
- `tacli plan` reads `plan.md` from the workspace root.
- background jobs require `--dangerously` because they cannot pause for interactive approvals.
