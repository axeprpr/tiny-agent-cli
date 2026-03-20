# onek-agent

`onek-agent` is a lean terminal coding agent for people who want the useful part of `claude` or `codex`, without the heavy stack.

It is built around one simple idea:

- small binary
- OpenAI-compatible endpoint
- shell + files + grep + fetch + web search
- interactive chat mode
- command and file-write confirmation by default
- `--dangerously` when you want speed

Chinese version: [README.zh-CN.md](/root/1k-install/README.zh-CN.md)

## Why It Exists

Most agent CLIs are powerful, but they also come with more runtime, more dependencies, and more surface area than many people actually need.

`onek-agent` aims for the opposite:

- one binary
- one model endpoint
- one workspace
- one task at a time, or one lightweight interactive session

It is not trying to be a full replacement for every agent platform. It is trying to be the cheap, practical one you can actually keep around.

## Core Ideas

- `PDCA` prompt discipline
  The built-in prompt nudges the model to plan, do, check, and adjust.
- `ReAct` tool use
  The model is pushed to inspect first, act second, verify after.
- `Safe by default`
  Shell commands require confirmation unless you opt into `--dangerously`.
- `Raw by default`
  `run` returns the model's native answer by default.
- `Terminal mode when wanted`
  Use `--output terminal` when you want a cleaner plain-text rendering.

## Commands

- `onek run [flags] <task>`
  Run one task
- `onek chat [flags]`
  Stay in a lightweight multi-turn session
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

Replace the endpoint and model with your own values.

Linux x86_64:

```bash
curl -L https://github.com/axeprpr/onek-agent/releases/latest/download/onek-linux-amd64 -o ./onek && chmod +x ./onek && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='qwen2.5-coder:7b' ./onek chat
```

Linux arm64:

```bash
curl -L https://github.com/axeprpr/onek-agent/releases/latest/download/onek-linux-arm64 -o ./onek && chmod +x ./onek && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='qwen2.5-coder:7b' ./onek chat
```

macOS Intel:

```bash
curl -L https://github.com/axeprpr/onek-agent/releases/latest/download/onek-darwin-amd64 -o ./onek && chmod +x ./onek && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='qwen2.5-coder:7b' ./onek chat
```

macOS Apple Silicon:

```bash
curl -L https://github.com/axeprpr/onek-agent/releases/latest/download/onek-darwin-arm64 -o ./onek && chmod +x ./onek && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='qwen2.5-coder:7b' ./onek chat
```

Windows PowerShell x64:

```powershell
$env:MODEL_BASE_URL='http://127.0.0.1:11434/v1'; $env:MODEL_NAME='qwen2.5-coder:7b'; Invoke-WebRequest https://github.com/axeprpr/onek-agent/releases/latest/download/onek-windows-amd64.exe -OutFile .\onek.exe; .\onek.exe chat
```

Windows PowerShell arm64:

```powershell
$env:MODEL_BASE_URL='http://127.0.0.1:11434/v1'; $env:MODEL_NAME='qwen2.5-coder:7b'; Invoke-WebRequest https://github.com/axeprpr/onek-agent/releases/latest/download/onek-windows-arm64.exe -OutFile .\onek.exe; .\onek.exe chat
```

With your endpoint:

```bash
curl -L https://github.com/axeprpr/onek-agent/releases/latest/download/onek-linux-amd64 -o ./onek && chmod +x ./onek && MODEL_BASE_URL='https://llm.haohuapm.com:20020' MODEL_NAME='Qwen3.5-27B-FP8' ./onek chat
```

## One-Line Install

Linux or macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/axeprpr/onek-agent/main/scripts/install.sh | bash
```

Windows PowerShell:

```powershell
iwr https://raw.githubusercontent.com/axeprpr/onek-agent/main/scripts/install.ps1 -UseBasicParsing | iex
```

Optional install variables:

- `ONEK_VERSION`
  Install a specific tag like `v0.1.2`
- `ONEK_INSTALL_DIR`
  Install to a custom directory

## Interactive Chat

`chat` keeps context across turns, so it behaves much more like a small coding assistant than a one-shot CLI.

Built-in chat commands:

- `/help`
- `/status`
- `/reset`
- `/approval confirm|dangerously`
- `/output raw|terminal`
- `/model <name>`
- `/memory`
- `/remember <text>`
- `/forget <query>`
- `/exit`

Example:

```text
onek> inspect this repo
onek> what should I improve next?
onek> /approval dangerously
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

## Persistent Memory

`onek-agent` now has a lightweight persistent memory layer for long-lived usage.

Use it to store user preferences, project rules, or stable context:

```text
onek> /remember Prefer concise answers in Chinese.
onek> /remember This repo targets ARM64 first.
onek> /memory
onek> /forget ARM64
```

Memory is stored in:

- `.onek-agent/memory.json`

That memory is injected into future chat sessions as background context.

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
