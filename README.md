# tiny-agent-cli

`tiny-agent-cli` is a lean terminal coding agent for people who want a cheap `codex` or `claude-cli` style workflow, without the heavy stack.

It is built for offline-friendly and low-dependency environments:

- one small binary
- no Node.js
- no Python runtime requirement
- no Electron
- no background service
- works on Linux, macOS, and Windows
- only depends on one OpenAI-compatible LLM endpoint, including your own local model server

You can think of it as:

- a cheap `codex`
- a stripped-down `claude-cli`
- a terminal-native agent you can drop into a machine and run immediately

Chinese version: [README.zh-CN.md](README.zh-CN.md)

## Why It Exists

Most agent CLIs are powerful, but they also come with more runtime, more dependencies, and more installation friction than many people actually need.

That is a bad fit for:

- offline or semi-offline machines
- servers you do not want to pollute with extra runtimes
- minimal containers and rescue environments
- users who just want a binary plus a model endpoint

`tiny-agent-cli` aims for the opposite:

- one binary
- one model endpoint
- no Node.js dependency
- one workspace
- one task at a time, or one lightweight interactive session

It is not trying to be the biggest agent platform. It is trying to be the one you can actually keep around on every box.

## What You Get

- shell + files + grep + fetch + web search
- interactive chat mode with SSE streaming support
- background jobs for parallel exploration
- persistent memory with global and project scope
- command and file-write confirmation by default
- `--dangerously` when you want speed
- raw binary releases for direct download, no zip or tgz step

## Core Ideas

- `PDCA` prompt discipline
  The built-in prompt nudges the model to plan, do, check, and adjust.
- `ReAct` tool use
  The model is pushed to inspect first, act second, verify after.
- `Safe by default`
  Shell commands require confirmation unless you opt into `--dangerously`.
- `Scoped memory`
  Keep global preferences and project-specific rules without dragging all context everywhere.
- `Raw by default`
  `run` returns the model's native answer by default.
- `Terminal mode when wanted`
  `chat` uses terminal-friendly rendering by default while `run` stays raw.

## Commands

- `tacli`
  Default chat on interactive terminals
- `tacli -d`
  Default chat in dangerously mode
- `tacli run [--dangerously] <task>`
  Run one task
- `tacli <task>`
  Shorthand for a one-shot run
- `tacli chat`
  Stay in a lightweight multi-turn session with a full-screen terminal UI on interactive terminals
- `tacli models`
  List available models from the endpoint
- `tacli ping`
  Quick endpoint and model check
- `tacli version`
  Print the embedded version

## Approval Modes

- `confirm`
  Default. Shell commands and file writes ask for confirmation.
- `dangerously`
  Skip shell and file-write prompts for the current run or chat session.

Examples:

```bash
tacli
tacli -d
tacli "inspect this repo and tell me what it does"
tacli -d "run go test ./..."
tacli run --dangerously "run go test ./..."
tacli chat --dangerously
```

## One-Line Install

The installer script auto-detects the architecture, so each platform only needs one command.

Linux or macOS:

```bash
curl -fsSL https://gh-proxy.com/https://raw.githubusercontent.com/axeprpr/tiny-agent-cli/main/scripts/install.sh | bash && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='your-model' ~/.local/bin/tacli
```

Windows PowerShell:

```powershell
iwr https://gh-proxy.com/https://raw.githubusercontent.com/axeprpr/tiny-agent-cli/main/scripts/install.ps1 -UseBasicParsing | iex; $env:MODEL_BASE_URL='http://127.0.0.1:11434/v1'; $env:MODEL_NAME='your-model'; $HOME\.local\bin\tacli.exe
```

Optional install variables:

- `TACLI_VERSION`
  Install a specific tag like `v0.1.2`
- `TACLI_INSTALL_DIR`
  Install to a custom directory

## Interactive Chat

`chat` keeps context across turns, so it behaves much more like a small coding assistant than a one-shot CLI.

On interactive terminals, `chat` now opens a full-screen TUI with:

- top info bar for workspace, shell, model, and approval mode
- single-column conversation view with inline step/tool activity
- SSE streaming: assistant tokens appear in real-time as the model generates them
- optional activity drawer for filtered step/tool/error/approval logs
- dynamic multi-line composer that grows with your input (1-5 lines)
- richer assistant rendering for markdown-style answers and code blocks
- footer status bar for model, approval mode, session, and approximate context remaining
- inline command/file approval prompts in the conversation flow
- `Ctrl+O` to toggle the activity drawer
- `Ctrl+G` to cycle activity filters
- `Home` / `End` to jump to top/bottom of conversation
- `PgUp` / `PgDn` to scroll
- `F1` to toggle help

Built-in chat commands:

- `/help`                 Show all commands
- `/exit`, `/quit`        Exit the chat
- `/reset`                Clear conversation context
- `/session [name|new]`   Switch or create a session
- `/status`               Show session and config status
- `/scope`                Show current project scope key
- `/model <name>`         Switch model for this session
- `/approval <mode>`      Set approval mode (confirm|dangerously)
- `/memory`               Show saved memory notes
- `/remember <text>`      Save a project memory note
- `/remember-global <t>`  Save a global memory note
- `/forget <query>`       Remove matching project memory
- `/forget-global <q>`    Remove matching global memory
- `/memorize`             Extract memory from conversation
- `/bg <task>`            Start a background job
- `/jobs`                 List background jobs
- `/job <id>`             Inspect a background job
- `/job-send <id> <msg>`  Send follow-up to a background job
- `/job-cancel <id>`      Cancel a background job
- `/job-apply <id>`       Apply job result to chat context

Example:

```text
tacli> inspect this repo
tacli> what should I improve next?
tacli> /approval dangerously
tacli> /remember Prefer concise answers.
tacli> /remember-global Always answer in English unless asked otherwise.
tacli> /bg analyze all error handling paths in this repo
tacli> /jobs
tacli> /memorize
tacli> /reset
tacli> write a minimal release checklist
```

## Session Persistence

`chat` sessions are persisted under `.tacli` by default.

- session state:
  `.tacli/sessions/<session>.json`
- transcript log:
  `.tacli/transcripts/<session>.log`

By default, each `chat` launch starts a fresh timestamped session and auto-summarizes stable memory on exit. Use `tacli chat --session <name>` or `/session <name>` to resume or switch sessions.
Advanced storage tuning is still available through environment variables like `AGENT_STATE_DIR`.

## Persistent Memory

`tiny-agent-cli` now has a lightweight persistent memory layer for long-lived usage.

It supports two scopes:

- global memory
  Cross-project preferences, such as response style or language
- project memory
  Notes tied to the current workspace path

Use it to store user preferences, project rules, or stable context:

```text
tacli> /remember-global Prefer concise answers in Chinese.
tacli> /remember This repo targets ARM64 first.
tacli> /scope
tacli> /memory
tacli> /memorize
tacli> /forget ARM64
```

Memory is stored in:

- `.tacli/memory.json`

Future chat sessions inject matching memory into the first system prompt as background context.

Notes:

- project memory is keyed by workspace path
- `/memorize` summarizes the current session into project memory
- chat runs that summarization automatically on exit
- if the model-side memory summarizer fails, `tiny-agent-cli` falls back to local extraction of obvious stable preferences and project facts
- long conversations are compacted into a local synthetic summary while keeping recent full turns, which helps shorter-context models survive longer sessions
- session and memory files use atomic writes (write to temp file, then rename) to prevent corruption on crash

## Background Jobs

In `dangerously` mode, you can run parallel subtasks in the background while continuing your main conversation:

```text
tacli> /bg explore the test coverage of this repo
tacli> /jobs
tacli> /job job-001
tacli> /job-apply job-001
```

The agent can also start background jobs automatically for broad exploration tasks. Background job results are injected into the main conversation context when ready.

Up to 2 background jobs can run concurrently. Each runs in its own agent session with full tool access.

## Environment Variables

- `MODEL_BASE_URL`
  Default: `http://127.0.0.1:11434/v1`
- `MODEL_NAME`
  Default: `qwen2.5-coder:7b`
- `MODEL_API_KEY`
  Default: empty
- `MODEL_TIMEOUT`
  Default: `180s`. Maximum time to wait for a model response.
- `MODEL_CONTEXT_WINDOW`
  Default: `32768`. Used for context usage estimation in the TUI.
- `AGENT_WORKDIR`
  Default: current directory
- `AGENT_MAX_STEPS`
  Default: `24`
- `AGENT_COMMAND_TIMEOUT`
  Default: `30s`
- `AGENT_SHELL`
  Default: `bash` on Linux/macOS, `powershell.exe` on Windows
- `AGENT_APPROVAL`
  Default: `confirm`

## Build From Source

```bash
go test ./...
go build ./...
go run ./cmd/tacli version
```

## Release

Local build:

```bash
./scripts/build-release.sh v0.1.2
```

GitHub release:

- push a tag like `v0.1.2`
- GitHub Actions builds raw binaries for `linux`, `darwin`, and `windows`
- release assets are uploaded as direct binaries, not archives

## Status

This is now more than a toy, but still intentionally small.

If you want a cheap, hackable, terminal-native coding agent, this is the point.
