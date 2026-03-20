# onek-agent

`onek-agent` is a minimal Go prototype for a single-task terminal agent.

The first version is intentionally small:

- One task per run
- OpenAI-compatible chat API
- Basic tool calling loop
- Shell, file, grep, URL fetch, and lightweight web search tools
- Workspace-scoped file access

## Quick Start

1. Start any OpenAI-compatible local endpoint.
2. Set the model if you do not want the default:

```bash
export MODEL_NAME=qwen2.5-coder:7b
```

3. Run the agent:

```bash
go run ./cmd/onek run "inspect this repo and summarize the next steps"
```

## Environment Variables

- `MODEL_BASE_URL`
  Default: `http://127.0.0.1:11434/v1`
- `MODEL_NAME`
  Default: `qwen2.5-coder:7b`
- `MODEL_API_KEY`
  Default: empty
- `AGENT_WORKDIR`
  Default: current working directory
- `AGENT_MAX_STEPS`
  Default: `8`
- `AGENT_COMMAND_TIMEOUT`
  Default: `30s`
- `AGENT_SHELL`
  Default: platform shell (`bash` on Linux, `powershell.exe` on Windows)

## Layout

- `cmd/onek`
  CLI entrypoint
- `internal/agent`
  Agent loop and system prompt
- `internal/config`
  Runtime configuration
- `internal/model`
  Shared request and response types
- `internal/model/ollama`
  Ollama-compatible API client
- `internal/tools`
  Built-in tools and workspace guardrails
- `docs/plan.md`
  Iteration plan for the next versions

## Status

This is a working prototype scaffold, not a production-safe agent.
The current loop is designed to be easy to extend, not fully hardened.

Ollama is just one backend example. The client targets the common OpenAI-style `/v1/chat/completions` surface so you can point it at Ollama, LM Studio, vLLM, or a hosted provider.
