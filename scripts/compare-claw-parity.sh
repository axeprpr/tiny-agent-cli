#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STATE_DIR="${ROOT_DIR}/.tacli/parity"
BIN_DIR="${STATE_DIR}/bin"
WORKDIR="${STATE_DIR}/workspace"
RUNS_DIR="${STATE_DIR}/runs"
PROBE_DIR="${STATE_DIR}/claw-runtime-probe"
OUTDIR="${RUNS_DIR}/$(date -u +%Y%m%d-%H%M%S)"

mkdir -p "${BIN_DIR}" "${WORKDIR}" "${RUNS_DIR}" "${PROBE_DIR}"
mkdir -p "${OUTDIR}"
cd "${ROOT_DIR}"

if [[ -n "${PARITY_ENV_FILE:-}" ]]; then
  set -a
  # shellcheck disable=SC1090
  source "${PARITY_ENV_FILE}"
  set +a
fi

if [[ -z "${MODEL_BASE_URL:-}" && -n "${SILICONFLOW_API_URL:-}" ]]; then
  MODEL_BASE_URL="${SILICONFLOW_API_URL%/chat/completions}"
fi
if [[ -z "${MODEL_API_KEY:-}" && -n "${SILICONFLOW_API_KEY:-}" ]]; then
  MODEL_API_KEY="${SILICONFLOW_API_KEY}"
fi
if [[ -z "${MODEL_NAME:-}" && -n "${SILICONFLOW_MODEL:-}" ]]; then
  MODEL_NAME="${SILICONFLOW_MODEL}"
fi

: "${MODEL_BASE_URL:?set MODEL_BASE_URL or provide PARITY_ENV_FILE with SILICONFLOW_API_URL}"
: "${MODEL_API_KEY:?set MODEL_API_KEY or provide PARITY_ENV_FILE with SILICONFLOW_API_KEY}"
: "${MODEL_NAME:?set MODEL_NAME or provide PARITY_ENV_FILE with SILICONFLOW_MODEL}"

if [[ -z "${GO_BIN:-}" && -x "/root/.local/go1.25.1/bin/go" ]]; then
  GO_BIN="/root/.local/go1.25.1/bin/go"
fi
if [[ -z "${CARGO_BIN:-}" && -x "${HOME:-/root}/.cargo/bin/cargo" ]]; then
  CARGO_BIN="${HOME:-/root}/.cargo/bin/cargo"
fi

GO_BIN="${GO_BIN:-go}"
CARGO_BIN="${CARGO_BIN:-cargo}"
NODE_BIN="${NODE_BIN:-node}"
TIMEOUT_BIN="${TIMEOUT_BIN:-timeout}"

GOPATH="${GOPATH:-${HOME:-/root}/go}"
GOMODCACHE="${GOMODCACHE:-${GOPATH}/pkg/mod}"
GOCACHE="${GOCACHE:-${HOME:-/root}/.cache/go-build}"
mkdir -p "${GOMODCACHE}" "${GOCACHE}"

if [[ -z "${CLAW_CODE_RUST_DIR:-}" ]]; then
  if [[ -d "/root/.openclaw/workspace/claw-code/rust" ]]; then
    CLAW_CODE_RUST_DIR="/root/.openclaw/workspace/claw-code/rust"
  else
    echo "CLAW_CODE_RUST_DIR is required when claw-code is not in the default local path." >&2
    exit 1
  fi
fi

create_probe_sources() {
  local cargo_toml="${PROBE_DIR}/Cargo.toml"
  local main_rs="${PROBE_DIR}/src/main.rs"
  mkdir -p "${PROBE_DIR}/src"

  cat >"${cargo_toml}" <<EOF
[package]
name = "claw-runtime-probe"
version = "0.1.0"
edition = "2021"

[dependencies]
api = { path = "${CLAW_CODE_RUST_DIR}/crates/api" }
runtime = { path = "${CLAW_CODE_RUST_DIR}/crates/runtime" }
tools = { path = "${CLAW_CODE_RUST_DIR}/crates/tools" }
serde_json = "1"
tokio = { version = "1", features = ["rt-multi-thread"] }
EOF

  cat >"${main_rs}" <<'EOF'
use std::env;
use std::error::Error;

use api::{
    max_tokens_for_model, InputContentBlock, InputMessage, MessageRequest, OutputContentBlock,
    ProviderClient, ToolChoice, ToolDefinition, ToolResultContentBlock,
};
use runtime::{
    ApiClient, ApiRequest, AssistantEvent, ContentBlock, ConversationMessage, ConversationRuntime,
    MessageRole, PermissionMode, PermissionPolicy, ProjectContext, RuntimeError, Session,
    SystemPromptBuilder, TokenUsage, ToolError, ToolExecutor,
};
use serde_json::{json, Value};
use tools::GlobalToolRegistry;

struct ProviderRuntimeClient {
    runtime: tokio::runtime::Runtime,
    client: ProviderClient,
    model: String,
    tool_definitions: Vec<ToolDefinition>,
}

impl ProviderRuntimeClient {
    fn new(model: String, tool_definitions: Vec<ToolDefinition>) -> Result<Self, Box<dyn Error>> {
        Ok(Self {
            runtime: tokio::runtime::Runtime::new()?,
            client: ProviderClient::from_model(&model)?,
            model,
            tool_definitions,
        })
    }
}

impl ApiClient for ProviderRuntimeClient {
    fn stream(&mut self, request: ApiRequest) -> Result<Vec<AssistantEvent>, RuntimeError> {
        let message_request = MessageRequest {
            model: self.model.clone(),
            max_tokens: max_tokens_for_model(&self.model),
            messages: convert_messages(&request.messages),
            system: (!request.system_prompt.is_empty()).then(|| request.system_prompt.join("\n\n")),
            tools: Some(self.tool_definitions.clone()),
            tool_choice: Some(ToolChoice::Auto),
            stream: false,
        };

        let response = self
            .runtime
            .block_on(async { self.client.send_message(&message_request).await })
            .map_err(|error| RuntimeError::new(error.to_string()))?;

        let mut events = Vec::new();
        for block in response.content {
            match block {
                OutputContentBlock::Text { text } => events.push(AssistantEvent::TextDelta(text)),
                OutputContentBlock::ToolUse { id, name, input } => {
                    events.push(AssistantEvent::ToolUse {
                        id,
                        name,
                        input: serde_json::to_string(&input).unwrap_or_else(|_| "{}".to_string()),
                    });
                }
                OutputContentBlock::Thinking { .. } | OutputContentBlock::RedactedThinking { .. } => {}
            }
        }
        events.push(AssistantEvent::Usage(TokenUsage {
            input_tokens: response.usage.input_tokens,
            output_tokens: response.usage.output_tokens,
            cache_creation_input_tokens: response.usage.cache_creation_input_tokens,
            cache_read_input_tokens: response.usage.cache_read_input_tokens,
        }));
        events.push(AssistantEvent::MessageStop);
        Ok(events)
    }
}

struct RegistryToolExecutor {
    registry: GlobalToolRegistry,
}

impl ToolExecutor for RegistryToolExecutor {
    fn execute(&mut self, tool_name: &str, input: &str) -> Result<String, ToolError> {
        let value: Value = serde_json::from_str(input)
            .map_err(|error| ToolError::new(format!("invalid tool input JSON: {error}")))?;
        self.registry.execute(tool_name, &value).map_err(ToolError::new)
    }
}

fn convert_messages(messages: &[ConversationMessage]) -> Vec<InputMessage> {
    messages
        .iter()
        .filter_map(|message| {
            let role = match message.role {
                MessageRole::System | MessageRole::User | MessageRole::Tool => "user",
                MessageRole::Assistant => "assistant",
            };
            let content = message
                .blocks
                .iter()
                .map(|block| match block {
                    ContentBlock::Text { text } => InputContentBlock::Text { text: text.clone() },
                    ContentBlock::ToolUse { id, name, input } => InputContentBlock::ToolUse {
                        id: id.clone(),
                        name: name.clone(),
                        input: serde_json::from_str(input)
                            .unwrap_or_else(|_| json!({ "raw": input })),
                    },
                    ContentBlock::ToolResult {
                        tool_use_id,
                        output,
                        is_error,
                        ..
                    } => InputContentBlock::ToolResult {
                        tool_use_id: tool_use_id.clone(),
                        content: vec![ToolResultContentBlock::Text {
                            text: output.clone(),
                        }],
                        is_error: *is_error,
                    },
                })
                .collect::<Vec<_>>();
            (!content.is_empty()).then(|| InputMessage {
                role: role.to_string(),
                content,
            })
        })
        .collect()
}

fn build_permission_policy(mode: PermissionMode, registry: &GlobalToolRegistry) -> PermissionPolicy {
    registry
        .permission_specs(None)
        .into_iter()
        .fold(PermissionPolicy::new(mode), |policy, (name, required_permission)| {
            policy.with_tool_requirement(name, required_permission)
        })
}

fn extract_text(message: &ConversationMessage) -> String {
    let mut parts = Vec::new();
    for block in &message.blocks {
        if let ContentBlock::Text { text } = block {
            let trimmed = text.trim();
            if !trimmed.is_empty() {
                parts.push(trimmed.to_string());
            }
        }
    }
    parts.join("\n")
}

fn main() -> Result<(), Box<dyn Error>> {
    let prompt = env::args().skip(1).collect::<Vec<_>>().join(" ");
    if prompt.trim().is_empty() {
        return Err("missing prompt".into());
    }

    let model = env::var("CLAW_PROBE_MODEL")?;
    let cwd = env::current_dir()?;
    let project_context = ProjectContext::discover_with_git(&cwd, "2026-04-09")?;
    let system_prompt = SystemPromptBuilder::new()
        .with_project_context(project_context)
        .with_os(env::consts::OS, "unknown")
        .build();

    let registry = GlobalToolRegistry::builtin();
    let tool_definitions = registry.definitions(None);
    let permission_policy = build_permission_policy(PermissionMode::DangerFullAccess, &registry);
    let api_client = ProviderRuntimeClient::new(model, tool_definitions)?;
    let tool_executor = RegistryToolExecutor { registry };

    let mut runtime = ConversationRuntime::new(
        Session::new(),
        api_client,
        tool_executor,
        permission_policy,
        system_prompt,
    );

    let summary = runtime.run_turn(prompt, None)?;
    let final_text = summary
        .assistant_messages
        .last()
        .map(extract_text)
        .unwrap_or_default();
    let tool_names = summary
        .assistant_messages
        .iter()
        .flat_map(|message| message.blocks.iter())
        .filter_map(|block| match block {
            ContentBlock::ToolUse { name, .. } => Some(name.clone()),
            _ => None,
        })
        .collect::<Vec<_>>();

    let payload = json!({
        "final_text": final_text,
        "iterations": summary.iterations,
        "tool_names": tool_names,
        "assistant_messages": summary.assistant_messages,
        "tool_results": summary.tool_results,
        "usage": {
            "input_tokens": summary.usage.input_tokens,
            "output_tokens": summary.usage.output_tokens,
            "cache_creation_input_tokens": summary.usage.cache_creation_input_tokens,
            "cache_read_input_tokens": summary.usage.cache_read_input_tokens,
        },
        "session_messages": runtime.session().messages,
        "cwd": cwd,
    });
    println!("{}", serde_json::to_string_pretty(&payload)?);
    Ok(())
}
EOF
}

build_probe() {
  create_probe_sources
  env HOME="${HOME:-/root}" "${CARGO_BIN}" build --release --manifest-path "${PROBE_DIR}/Cargo.toml" >/dev/null
  echo "${PROBE_DIR}/target/release/claw-runtime-probe"
}

build_tiny() {
  (
    cd "${ROOT_DIR}"
    env HOME="${HOME:-/root}" \
      GOPATH="${GOPATH}" \
      GOMODCACHE="${GOMODCACHE}" \
      GOCACHE="${GOCACHE}" \
      "${GO_BIN}" build -o "${BIN_DIR}/tacli-dev" ./cmd/tacli >/dev/null
  )
  echo "${BIN_DIR}/tacli-dev"
}

prepare_workspace() {
  rm -rf "${WORKDIR}"
  mkdir -p "${WORKDIR}"
  printf 'hello parity\n' > "${WORKDIR}/note.txt"
}

run_case() {
  local claw_bin="$1"
  local tiny_bin="$2"
  local case_id="$3"
  local prompt="$4"

  rm -f "${WORKDIR}/hello.py"

  local claw_status=0
  local tiny_status=0

  set +e
  (
    cd "${WORKDIR}" &&
      env HOME="${HOME:-/root}" \
        OPENAI_API_KEY="${MODEL_API_KEY}" \
        OPENAI_BASE_URL="${MODEL_BASE_URL}" \
        CLAW_PROBE_MODEL="${MODEL_NAME}" \
        "${TIMEOUT_BIN}" 90s "${claw_bin}" "${prompt}" > "${OUTDIR}/${case_id}.claw.json"
  ) 2> "${OUTDIR}/${case_id}.claw.stderr"
  claw_status=$?

  (
    cd "${WORKDIR}" &&
      env HOME="${HOME:-/root}" \
        MODEL_BASE_URL="${MODEL_BASE_URL}" \
        MODEL_NAME="${MODEL_NAME}" \
        MODEL_API_KEY="${MODEL_API_KEY}" \
        "${TIMEOUT_BIN}" 90s "${tiny_bin}" run --dangerously --output jsonl \
          --base-url "${MODEL_BASE_URL}" \
          --model "${MODEL_NAME}" \
          --api-key "${MODEL_API_KEY}" \
          "${prompt}" > "${OUTDIR}/${case_id}.tiny.jsonl"
  ) 2> "${OUTDIR}/${case_id}.tiny.stderr"
  tiny_status=$?
  set -e

  if [[ -f "${WORKDIR}/hello.py" ]]; then
    cp "${WORKDIR}/hello.py" "${OUTDIR}/${case_id}.hello.py"
  fi

  printf '%s\n' "${claw_status}" > "${OUTDIR}/${case_id}.claw.status"
  printf '%s\n' "${tiny_status}" > "${OUTDIR}/${case_id}.tiny.status"
}

write_summary() {
  env OUTDIR="${OUTDIR}" WORKDIR="${WORKDIR}" "${NODE_BIN}" <<'EOF'
const fs = require('fs');
const outdir = process.env.OUTDIR;
const workdir = process.env.WORKDIR || '';
const cases = [
  { id: 'qa', prompt: 'Reply with exactly: parity-qa-ok', kind: 'exact', expected: 'parity-qa-ok' },
  { id: 'read', prompt: 'Read note.txt and reply with exactly its contents.', kind: 'exact', expected: 'hello parity' },
  { id: 'shell', prompt: 'Use a shell command to print the current working directory, then reply with exactly the path.', kind: 'exact', expected: workdir },
  { id: 'write', prompt: 'Create hello.py that prints hi-parity and then reply with exactly: file-created.', kind: 'exact', expected: 'file-created' },
  { id: 'web', prompt: 'Find the GitHub repository URL for tiny-agent-cli and reply with just the URL.', kind: 'repo-url', expected: '' },
];

function readStatus(path) {
  return Number(fs.readFileSync(path, 'utf8').trim() || '0');
}

function readIfExists(path) {
  return fs.existsSync(path) ? fs.readFileSync(path, 'utf8') : '';
}

function parseTiny(path) {
  if (!fs.existsSync(path) || !fs.readFileSync(path, 'utf8').trim()) {
    return { final: '', tools: [] };
  }
  let lines = [];
  try {
    lines = fs.readFileSync(path, 'utf8').trim().split(/\n+/).filter(Boolean).map(line => JSON.parse(line));
  } catch {
    return { final: '', tools: [] };
  }
  const result = lines.find(event => event.type === 'result');
  const tools = [...new Set(lines.filter(event => event.type === 'tool_start').map(event => event.data?.name).filter(Boolean))];
  return { final: result?.data?.final || '', tools };
}

function parseClaw(path) {
  if (!fs.existsSync(path) || !fs.readFileSync(path, 'utf8').trim()) {
    return { final: '', tools: [] };
  }
  let payload = {};
  try {
    payload = JSON.parse(fs.readFileSync(path, 'utf8'));
  } catch {
    return { final: '', tools: [] };
  }
  return { final: payload.final_text || '', tools: payload.tool_names || [] };
}

function normalize(value) {
  return String(value || '').trim().replace(/\s+/g, ' ');
}

function checkExpected(kind, actual, expected) {
  const normalized = normalize(actual);
  if (kind === 'exact') return normalized === normalize(expected);
  if (kind === 'repo-url') return /^https?:\/\/github\.com\/[^/\s]+\/tiny-agent-cli\/?$/i.test(normalized);
  return false;
}

const summary = cases.map((entry) => {
  const clawStatus = readStatus(`${outdir}/${entry.id}.claw.status`);
  const tinyStatus = readStatus(`${outdir}/${entry.id}.tiny.status`);
  const claw = parseClaw(`${outdir}/${entry.id}.claw.json`);
  const tiny = parseTiny(`${outdir}/${entry.id}.tiny.jsonl`);
  const clawStderr = readIfExists(`${outdir}/${entry.id}.claw.stderr`).trim();
  const tinyStderr = readIfExists(`${outdir}/${entry.id}.tiny.stderr`).trim();
  const clawPass = clawStatus === 0 && !clawStderr && checkExpected(entry.kind, claw.final, entry.expected);
  const tinyPass = tinyStatus === 0 && !tinyStderr && checkExpected(entry.kind, tiny.final, entry.expected);
  const pairMatch = normalize(claw.final) === normalize(tiny.final);
  return {
    id: entry.id,
    prompt: entry.prompt,
    expected_kind: entry.kind,
    expected_value: entry.expected,
    claw_status: clawStatus,
    claw_final: claw.final,
    claw_tools: claw.tools,
    claw_stderr: clawStderr,
    claw_pass: clawPass,
    tiny_status: tinyStatus,
    tiny_final: tiny.final,
    tiny_tools: tiny.tools,
    tiny_stderr: tinyStderr,
    tiny_pass: tinyPass,
    pair_match: pairMatch,
  };
});

fs.writeFileSync(`${outdir}/summary.json`, JSON.stringify(summary, null, 2));

const lines = ['id\tclaw_pass\tclaw_status\tclaw_final\tclaw_tools\ttiny_pass\ttiny_status\ttiny_final\ttiny_tools\tpair_match'];
for (const row of summary) {
  lines.push([
    row.id,
    row.claw_pass,
    row.claw_status,
    row.claw_final.replace(/\s+/g, ' '),
    row.claw_tools.join(','),
    row.tiny_pass,
    row.tiny_status,
    row.tiny_final.replace(/\s+/g, ' '),
    row.tiny_tools.join(','),
    row.pair_match,
  ].join('\t'));
}
fs.writeFileSync(`${outdir}/summary.tsv`, lines.join('\n') + '\n');
console.log(JSON.stringify({ outdir, summary }, null, 2));
EOF
}

main() {
  local claw_bin
  local tiny_bin

  claw_bin="$(build_probe)"
  tiny_bin="$(build_tiny)"
  prepare_workspace

  run_case "${claw_bin}" "${tiny_bin}" "qa" "Reply with exactly: parity-qa-ok"
  run_case "${claw_bin}" "${tiny_bin}" "read" "Read note.txt and reply with exactly its contents."
  run_case "${claw_bin}" "${tiny_bin}" "shell" "Use a shell command to print the current working directory, then reply with exactly the path."
  run_case "${claw_bin}" "${tiny_bin}" "write" "Create hello.py that prints hi-parity and then reply with exactly: file-created."
  run_case "${claw_bin}" "${tiny_bin}" "web" "Find the GitHub repository URL for tiny-agent-cli and reply with just the URL."

  write_summary
  echo "summary: ${OUTDIR}/summary.json"
}

main "$@"
