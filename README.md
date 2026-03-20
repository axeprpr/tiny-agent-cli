# onek-agent

`onek-agent` is a lean terminal coding agent for people who want a cheap `codex` or `claude-cli` style workflow, without the heavy stack.

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

`onek-agent` aims for the opposite:

- one binary
- one model endpoint
- no Node.js dependency
- one workspace
- one task at a time, or one lightweight interactive session

It is not trying to be the biggest agent platform. It is trying to be the one you can actually keep around on every box.

## What You Get

- shell + files + grep + fetch + web search
- interactive chat mode
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
  Use `--output terminal` when you want a cleaner plain-text rendering.

## Commands

- `onek run [flags] <task>`
  Run one task
- `onek chat [flags]`
  Stay in a lightweight multi-turn session with a full-screen terminal UI on interactive terminals
- `onek models`
  List available models from the endpoint
- `onek ping`
  Quick endpoint and model check
- `onek version`
  Print the embedded version

## Approval Modes

- `confirm`
  Default. Shell commands and file writes ask for confirmation.
- `dangerously`
  Skip shell and file-write prompts for the current run or chat session.

Examples:

```bash
onek run --approval confirm "inspect this repo and tell me what it does"
onek run --dangerously "run go test ./..."
onek chat --dangerously
```

## One-Line Run

Replace the endpoint and model with your own values. The examples below do not depend on Node.js, Python, or any extra runtime.

Linux x86_64:

```bash
curl -L https://gh-proxy.com/https://github.com/axeprpr/onek-agent/releases/latest/download/onek-linux-amd64 -o ./onek && chmod +x ./onek && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='your-model' ./onek chat --auto-memory
```

Linux arm64:

```bash
curl -L https://gh-proxy.com/https://github.com/axeprpr/onek-agent/releases/latest/download/onek-linux-arm64 -o ./onek && chmod +x ./onek && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='your-model' ./onek chat --auto-memory
```

Linux x86_64 with `wget`:

```bash
wget -O ./onek https://gh-proxy.com/https://github.com/axeprpr/onek-agent/releases/latest/download/onek-linux-amd64 && chmod +x ./onek && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='your-model' ./onek chat --auto-memory
```

macOS Intel:

```bash
curl -L https://gh-proxy.com/https://github.com/axeprpr/onek-agent/releases/latest/download/onek-darwin-amd64 -o ./onek && chmod +x ./onek && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='your-model' ./onek chat --auto-memory
```

macOS Apple Silicon:

```bash
curl -L https://gh-proxy.com/https://github.com/axeprpr/onek-agent/releases/latest/download/onek-darwin-arm64 -o ./onek && chmod +x ./onek && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='your-model' ./onek chat --auto-memory
```

Windows PowerShell x64:

```powershell
$env:MODEL_BASE_URL='http://127.0.0.1:11434/v1'; $env:MODEL_NAME='your-model'; Invoke-WebRequest https://gh-proxy.com/https://github.com/axeprpr/onek-agent/releases/latest/download/onek-windows-amd64.exe -OutFile .\onek.exe; .\onek.exe chat --auto-memory
```

Windows PowerShell arm64:

```powershell
$env:MODEL_BASE_URL='http://127.0.0.1:11434/v1'; $env:MODEL_NAME='your-model'; Invoke-WebRequest https://gh-proxy.com/https://github.com/axeprpr/onek-agent/releases/latest/download/onek-windows-arm64.exe -OutFile .\onek.exe; .\onek.exe chat --auto-memory
```

## One-Line Install

Linux or macOS:

```bash
curl -fsSL https://gh-proxy.com/https://raw.githubusercontent.com/axeprpr/onek-agent/main/scripts/install.sh | bash
```

Windows PowerShell:

```powershell
iwr https://gh-proxy.com/https://raw.githubusercontent.com/axeprpr/onek-agent/main/scripts/install.ps1 -UseBasicParsing | iex
```

Install and start immediately:

Linux or macOS:

```bash
curl -fsSL https://gh-proxy.com/https://raw.githubusercontent.com/axeprpr/onek-agent/main/scripts/install.sh | bash && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='your-model' ~/.local/bin/onek chat --auto-memory
```

Windows PowerShell:

```powershell
iwr https://gh-proxy.com/https://raw.githubusercontent.com/axeprpr/onek-agent/main/scripts/install.ps1 -UseBasicParsing | iex; $env:MODEL_BASE_URL='http://127.0.0.1:11434/v1'; $env:MODEL_NAME='your-model'; $HOME\.local\bin\onek.exe chat --auto-memory
```

Optional install variables:

- `ONEK_VERSION`
  Install a specific tag like `v0.1.2`
- `ONEK_INSTALL_DIR`
  Install to a custom directory

## Interactive Chat

`chat` keeps context across turns, so it behaves much more like a small coding assistant than a one-shot CLI.

On interactive terminals, `chat` now opens a full-screen TUI with:

- top info bar for workspace, shell, model, and approval mode
- single-column conversation view
- collapsible activity drawer for step/tool/error/approval logs
- multiline input box with a dedicated composer area
- richer assistant rendering for markdown-style answers and code blocks
- footer status bar for model, approval mode, session, and approximate context remaining
- structured command/file approval prompts
- `Ctrl+O` to toggle the activity drawer
- `Ctrl+G` to cycle activity filters
- `F1` to toggle help

Built-in chat commands:

- `/help`
- `/status`
- `/reset`
- `/approval confirm|dangerously`
- `/output raw|terminal`
- `/model <name>`
- `/scope`
- `/memory`
- `/remember-global <text>`
- `/remember <text>`
- `/forget-global <query>`
- `/forget <query>`
- `/memorize`
- `/exit`

Example:

```text
onek> inspect this repo
onek> what should I improve next?
onek> /approval dangerously
onek> /remember Prefer concise answers.
onek> /remember-global Always answer in English unless asked otherwise.
onek> /memorize
onek> /output terminal
onek> /reset
onek> write a minimal release checklist
```

## Session Persistence

`chat` sessions are persisted under `.onek-agent` by default.

- session state:
  `.onek-agent/sessions/<session>.json`
- transcript log:
  `.onek-agent/transcripts/<session>.log`

Useful flags:

- `--session <name>`
  Choose a named session
- `--state-dir <path>`
  Move session and transcript files somewhere else
- `--auto-memory`
  Summarize stable notes from the session when chat exits

## Persistent Memory

`onek-agent` now has a lightweight persistent memory layer for long-lived usage.

It supports two scopes:

- global memory
  Cross-project preferences, such as response style or language
- project memory
  Notes tied to the current workspace path

Use it to store user preferences, project rules, or stable context:

```text
onek> /remember-global Prefer concise answers in Chinese.
onek> /remember This repo targets ARM64 first.
onek> /scope
onek> /memory
onek> /memorize
onek> /forget ARM64
```

Memory is stored in:

- `.onek-agent/memory.json`

Future chat sessions inject matching memory into the first system prompt as background context.

Notes:

- project memory is keyed by workspace path
- `/memorize` summarizes the current session into project memory
- `--auto-memory` runs that summarization automatically on chat exit
- if the model-side memory summarizer fails, `onek-agent` falls back to local extraction of obvious stable preferences and project facts
- long conversations are compacted into a local synthetic summary while keeping recent full turns, which helps shorter-context models survive longer sessions

## Output Modes

- `--output raw`
  Default. Keep the model's native answer.
- `--output terminal`
  Apply light formatting for terminals, mainly to reduce ugly Markdown tables.

Examples:

```bash
onek run --output raw "summarize this system"
onek run --output terminal "summarize this system"
```

## Environment Variables

- `MODEL_BASE_URL`
  Default: `http://127.0.0.1:11434/v1`
- `MODEL_NAME`
  Default: `qwen2.5-coder:7b`
- `MODEL_API_KEY`
  Default: empty
- `AGENT_WORKDIR`
  Default: current directory
- `AGENT_MAX_STEPS`
  Default: `8`
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
go run ./cmd/onek version
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
