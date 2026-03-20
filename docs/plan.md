# Go Version "Cheap Agent" Plan

## Goal

Build a small terminal agent that can:

- Accept one task from the CLI
- Use a local or open model through an OpenAI-compatible API
- Search the web
- Read and write files inside a workspace
- Execute bounded shell commands
- Finish after a single task run

## Non-Goals for v0

- Multi-session chat
- Rich TUI
- Background daemons
- Full MCP support
- Autonomous long-running orchestration

## Proposed Iterations

### v0.1 - Runnable Skeleton

- CLI entrypoint with `run` command
- Env-driven config
- OpenAI-compatible chat client
- Basic tool loop with max-step limit
- Built-in tools:
  - `list_files`
  - `read_file`
  - `write_file`
  - `grep`
  - `run_command`
  - `fetch_url`
  - `web_search`

### v0.2 - Hardening

- Denylist and allowlist rules for shell commands
- Output truncation policy
- Structured transcript logging
- Better text extraction for fetched web pages
- Safer file overwrite policy

### v0.3 - Better Planning

- Explicit planner/executor prompt split
- Retry policy for malformed tool arguments
- Small subtask abstraction without multi-session state
- Better task summaries

### v0.4 - Windows and Packaging

- Native PowerShell behavior
- Cross-compiled binaries
- Config file support
- Installer scripts

## Architecture

### CLI

Parses task input and runtime flags, then wires config, model client, and tool registry.

### Agent Loop

Runs a bounded loop:

1. Send conversation and tool schema to the model
2. Execute requested tools sequentially
3. Append tool outputs back into the conversation
4. Stop on a final assistant response or max-step limit

### Model Adapter

Uses the OpenAI-compatible `/v1/chat/completions` shape so the same code can point at Ollama, LM Studio, vLLM, or hosted gateways.

### Tools

Built-in tools are plain Go code. That keeps the first version dependency-light and avoids pulling in Node or Python just for tool hosting.

## Immediate Next Work

1. Add transcript persistence under `.onek-agent/`
2. Add approval modes for shell and file writes
3. Improve `web_search` with a pluggable provider
4. Add tests around path safety and shell guardrails
