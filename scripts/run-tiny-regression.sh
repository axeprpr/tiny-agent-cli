#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STATE_DIR="${ROOT_DIR}/.tacli/tiny-regression"
BIN_DIR="${STATE_DIR}/bin"
RUNS_DIR="${STATE_DIR}/runs"
OUTDIR="${RUNS_DIR}/$(date -u +%Y%m%d-%H%M%S)"

mkdir -p "${BIN_DIR}" "${RUNS_DIR}" "${OUTDIR}"
cd "${ROOT_DIR}"

if [[ -n "${REGRESSION_ENV_FILE:-}" ]]; then
  set -a
  # shellcheck disable=SC1090
  source "${REGRESSION_ENV_FILE}"
  set +a
elif [[ -n "${PARITY_ENV_FILE:-}" ]]; then
  set -a
  # shellcheck disable=SC1090
  source "${PARITY_ENV_FILE}"
  set +a
elif [[ -f "/root/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "/root/.env"
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

: "${MODEL_BASE_URL:?set MODEL_BASE_URL or provide REGRESSION_ENV_FILE/PARITY_ENV_FILE}"
: "${MODEL_API_KEY:?set MODEL_API_KEY or provide REGRESSION_ENV_FILE/PARITY_ENV_FILE}"
: "${MODEL_NAME:?set MODEL_NAME or provide REGRESSION_ENV_FILE/PARITY_ENV_FILE}"

if [[ -z "${GO_BIN:-}" && -x "/root/.local/go1.25.1/bin/go" ]]; then
  GO_BIN="/root/.local/go1.25.1/bin/go"
fi

GO_BIN="${GO_BIN:-go}"
NODE_BIN="${NODE_BIN:-node}"
TIMEOUT_BIN="${TIMEOUT_BIN:-timeout}"

GOPATH="${GOPATH:-${HOME:-/root}/go}"
GOMODCACHE="${GOMODCACHE:-${GOPATH}/pkg/mod}"
GOCACHE="${GOCACHE:-${HOME:-/root}/.cache/go-build}"
mkdir -p "${GOMODCACHE}" "${GOCACHE}"

build_tiny() {
  (
    cd "${ROOT_DIR}"
    env HOME="${HOME:-/root}" \
      GOPATH="${GOPATH}" \
      GOMODCACHE="${GOMODCACHE}" \
      GOCACHE="${GOCACHE}" \
      "${GO_BIN}" build -o "${BIN_DIR}/tacli-regression" ./cmd/tacli >/dev/null
  )
  echo "${BIN_DIR}/tacli-regression"
}

prepare_workspace() {
  local workdir="$1"
  rm -rf "${workdir}"
  mkdir -p "${workdir}"
  {
    for i in $(seq 1 220); do
      printf 'filler line %03d for regression coverage\n' "${i}"
    done
    printf 'TARGET=amber-42\n'
  } > "${workdir}/manual.txt"
  cat > "${workdir}/profile.txt" <<'EOF'
owner: regression-bot
codename: atlas-9
EOF
  {
    for i in $(seq 1 260); do
      printf 'memory chunk %03d retains background detail but not the secret\n' "${i}"
    done
    printf 'PASSPHRASE=cedar-omega\n'
  } > "${workdir}/bigmemo.txt"
  {
    printf 'PASSPHRASE=cedar-omega\n'
    for i in $(seq 1 260); do
      printf 'compact memory chunk %03d adds noise after the secret\n' "${i}"
    done
  } > "${workdir}/compactmemo.txt"
}

run_case() {
  local tiny_bin="$1"
  local case_id="$2"
  local prompt="$3"
  local workdir="${OUTDIR}/${case_id}.workspace"
  local state_dir="${OUTDIR}/${case_id}.state"

  prepare_workspace "${workdir}"
  mkdir -p "${state_dir}"

  local status=0
  local attempt=1
  while true; do
    set +e
    (
      cd "${workdir}" &&
        env HOME="${HOME:-/root}" \
          AGENT_STATE_DIR="${state_dir}" \
          MODEL_BASE_URL="${MODEL_BASE_URL}" \
          MODEL_NAME="${MODEL_NAME}" \
          MODEL_API_KEY="${MODEL_API_KEY}" \
          "${TIMEOUT_BIN}" 120s "${tiny_bin}" run --dangerously --output jsonl \
            --base-url "${MODEL_BASE_URL}" \
            --model "${MODEL_NAME}" \
            --api-key "${MODEL_API_KEY}" \
            "${prompt}" > "${OUTDIR}/${case_id}.jsonl"
    ) 2> "${OUTDIR}/${case_id}.stderr"
    status=$?
    set -e
    if [[ "${status}" -ne 124 || "${attempt}" -ge 2 ]]; then
      break
    fi
    attempt=$((attempt + 1))
  done

  printf '%s\n' "${status}" > "${OUTDIR}/${case_id}.status"
}

run_chat_case() {
  local tiny_bin="$1"
  local case_id="$2"
  local session_name="$3"
  local context_window="$4"
  local input_text="$5"
  local state_key="${6:-$case_id}"
  local reset_workspace="${7:-yes}"
  local workdir="${OUTDIR}/${state_key}.workspace"
  local state_dir="${OUTDIR}/${state_key}.state"

  if [[ "${reset_workspace}" == "yes" ]]; then
    prepare_workspace "${workdir}"
  else
    mkdir -p "${workdir}" "${state_dir}"
  fi
  mkdir -p "${state_dir}"
  printf '%s' "${input_text}" > "${OUTDIR}/${case_id}.stdin"

  local status=0
  local attempt=1
  while true; do
    set +e
    (
      cd "${workdir}" &&
        env HOME="${HOME:-/root}" \
          AGENT_STATE_DIR="${state_dir}" \
          MODEL_BASE_URL="${MODEL_BASE_URL}" \
          MODEL_NAME="${MODEL_NAME}" \
          MODEL_API_KEY="${MODEL_API_KEY}" \
          MODEL_CONTEXT_WINDOW="${context_window}" \
          "${TIMEOUT_BIN}" 180s "${tiny_bin}" chat --dangerously --session "${session_name}" --output raw \
            --base-url "${MODEL_BASE_URL}" \
            --model "${MODEL_NAME}" \
            --api-key "${MODEL_API_KEY}" < "${OUTDIR}/${case_id}.stdin" > "${OUTDIR}/${case_id}.stdout"
    ) 2> "${OUTDIR}/${case_id}.stderr"
    status=$?
    set -e
    if [[ "${status}" -ne 124 || "${attempt}" -ge 2 ]]; then
      break
    fi
    attempt=$((attempt + 1))
  done

  printf '%s\n' "${status}" > "${OUTDIR}/${case_id}.status"
}

write_summary() {
  env OUTDIR="${OUTDIR}" "${NODE_BIN}" <<'EOF'
const fs = require('fs');
const path = require('path');
const outdir = process.env.OUTDIR;

const cases = [
  {
    id: 'run_long_extract',
    mode: 'run',
    kind: 'exact',
    expected: 'amber-42',
    toolsRequired: ['read_file'],
  },
  {
    id: 'chat_followup',
    mode: 'chat',
    kind: 'chat-final-exact',
    expected: 'atlas-9',
    stdoutMustContain: ['remembered', 'atlas-9'],
  },
  {
    id: 'chat_compact',
    mode: 'chat',
    kind: 'chat-final-exact',
    expected: 'cedar-omega',
    stdoutMustContain: ['noted', 'cedar-omega'],
  },
  {
    id: 'chat_status_cmd',
    mode: 'chat',
    kind: 'status-zero',
    stderrMustContain: ['session=status-case', 'memory_scope=', 'state=', 'trace='],
  },
  {
    id: 'chat_memory_cmd',
    mode: 'chat',
    kind: 'status-zero',
    stderrMustContain: ['project alias atlas-9', 'project_notes=1', 'project_notes=0'],
  },
  {
    id: 'chat_tasks_cmd',
    mode: 'chat',
    kind: 'status-zero',
    stderrMustContain: ['regression task alpha', 'pending'],
    filesMustExist: ['chat_tasks.workspace/.tacli/tasks.json'],
  },
  {
    id: 'chat_session_save_cmd',
    mode: 'chat',
    kind: 'status-zero',
    stderrMustContain: ['project memory saved', 'session saved'],
    filesMustExist: ['restore-seq.state/sessions/restore-case.json'],
    fileMustContain: [
      {
        path: 'restore-seq.state/sessions/restore-case.json',
        text: 'atlas-9',
      },
    ],
  },
  {
    id: 'chat_session_restore_cmd',
    mode: 'chat',
    kind: 'status-zero',
    stderrMustContain: ['session restored: restore-case', 'project alias atlas-9', 'project_notes=1'],
  },
  {
    id: 'chat_session_list_cmd',
    mode: 'chat',
    kind: 'status-zero',
    stderrMustContain: ['current=restore-case', 'recent:', '- restore-case'],
  },
];

function readStatus(caseId) {
  return Number(fs.readFileSync(path.join(outdir, `${caseId}.status`), 'utf8').trim() || '0');
}

function readText(filePath) {
  return fs.existsSync(filePath) ? fs.readFileSync(filePath, 'utf8') : '';
}

function normalize(value) {
  return String(value || '').trim().replace(/\s+/g, ' ');
}

function parseJsonl(caseId) {
  const filePath = path.join(outdir, `${caseId}.jsonl`);
  if (!fs.existsSync(filePath)) return { final: '', tools: [], toolCounts: {} };
  const raw = fs.readFileSync(filePath, 'utf8').trim();
  if (!raw) return { final: '', tools: [], toolCounts: {} };
  const events = raw.split(/\n+/).filter(Boolean).map((line) => JSON.parse(line));
  const result = events.find((event) => event.type === 'result');
  const toolCounts = {};
  for (const name of events.filter((event) => event.type === 'tool_start').map((event) => event.data?.name).filter(Boolean)) {
    toolCounts[name] = (toolCounts[name] || 0) + 1;
  }
  return {
    final: result?.data?.final || '',
    tools: Object.keys(toolCounts),
    toolCounts,
  };
}

function parseChat(caseId) {
  const filePath = path.join(outdir, `${caseId}.stdout`);
  const raw = readText(filePath);
  const lines = raw.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
  return {
    lines,
    final: lines.length > 0 ? lines[lines.length - 1] : '',
    stdout: raw,
  };
}

function readCaseArtifacts(caseId) {
  return {
    stdout: readText(path.join(outdir, `${caseId}.stdout`)),
    stderr: readText(path.join(outdir, `${caseId}.stderr`)),
  };
}

function hasRequiredTools(tools, required) {
  if (!required || required.length === 0) return true;
  return required.every((tool) => tools.includes(tool));
}

function checkCase(entry, parsed) {
  if (entry.kind === 'exact') {
    return normalize(parsed.final) === normalize(entry.expected);
  }
  if (entry.kind === 'chat-final-exact') {
    return normalize(parsed.final) === normalize(entry.expected);
  }
  if (entry.kind === 'chat-final-contains') {
    return normalize(parsed.final).includes(normalize(entry.expected));
  }
  if (entry.kind === 'status-zero') {
    return true;
  }
  return false;
}

function checkContains(haystack, needles) {
  if (!needles || needles.length === 0) return true;
  const normalizedHaystack = normalize(haystack);
  return needles.every((needle) => normalizedHaystack.includes(normalize(needle)));
}

function checkFilesExist(files) {
  if (!files || files.length === 0) return true;
  return files.every((file) => fs.existsSync(path.join(outdir, file)));
}

function checkFileContains(rules) {
  if (!rules || rules.length === 0) return true;
  return rules.every((rule) => {
    const fullPath = path.join(outdir, rule.path);
    if (!fs.existsSync(fullPath)) return false;
    return normalize(fs.readFileSync(fullPath, 'utf8')).includes(normalize(rule.text));
  });
}

const summary = cases.map((entry) => {
  const status = readStatus(entry.id);
  const artifacts = readCaseArtifacts(entry.id);
  const stderr = artifacts.stderr.trim();
  const parsed = entry.mode === 'run' ? parseJsonl(entry.id) : parseChat(entry.id);
  const toolsPass = entry.mode === 'run' ? hasRequiredTools(parsed.tools, entry.toolsRequired) : true;
  const stdoutPass = checkContains(parsed.stdout || parsed.final || '', entry.stdoutMustContain);
  const stderrPass = checkContains(artifacts.stderr, entry.stderrMustContain);
  const filesExistPass = checkFilesExist(entry.filesMustExist);
  const fileContainsPass = checkFileContains(entry.fileMustContain);
  const pass = status === 0 && checkCase(entry, parsed) && toolsPass && stdoutPass && stderrPass && filesExistPass && fileContainsPass;
  return {
    id: entry.id,
    mode: entry.mode,
    status,
    final: parsed.final,
    lines: parsed.lines || [],
    tools: parsed.tools || [],
    stderr,
    pass,
    tools_pass: toolsPass,
    stdout_pass: stdoutPass,
    stderr_pass: stderrPass,
    files_exist_pass: filesExistPass,
    file_contains_pass: fileContainsPass,
  };
});

fs.writeFileSync(path.join(outdir, 'summary.json'), JSON.stringify(summary, null, 2));

const lines = ['id\tmode\tpass\tstatus\tfinal\ttools\ttools_pass\tstdout_pass\tstderr_pass\tfiles_exist_pass\tfile_contains_pass'];
for (const row of summary) {
  lines.push([
    row.id,
    row.mode,
    row.pass,
    row.status,
    normalize(row.final),
    (row.tools || []).join(','),
    row.tools_pass,
    row.stdout_pass,
    row.stderr_pass,
    row.files_exist_pass,
    row.file_contains_pass,
  ].join('\t'));
}
fs.writeFileSync(path.join(outdir, 'summary.tsv'), lines.join('\n') + '\n');
console.log(JSON.stringify({ outdir, summary }, null, 2));
EOF
}

main() {
  local tiny_bin
  tiny_bin="$(build_tiny)"

  run_case "${tiny_bin}" "run_long_extract" "Read manual.txt and reply with exactly: amber-42"

  run_chat_case "${tiny_bin}" "chat_followup" "followup-case" "4096" $'Read profile.txt and remember the codename only. Reply with exactly: remembered.\nWhat is the codename? Reply with exactly the codename.\n'

  run_chat_case "${tiny_bin}" "chat_compact" "compact-case" "900" $'Read compactmemo.txt and remember the passphrase. Reply with exactly: noted.\n/compact\nWhat is the passphrase? Reply with exactly the passphrase.\n'
  run_chat_case "${tiny_bin}" "chat_status_cmd" "status-case" "4096" $'/status\n'
  run_chat_case "${tiny_bin}" "chat_memory_cmd" "memory-case" "4096" $'/remember project alias atlas-9\n/memory show\n/forget atlas-9\n/memory show\n'
  run_chat_case "${tiny_bin}" "chat_tasks_cmd" "tasks-case" "4096" $'/tasks create regression task alpha\n/tasks list\n' "chat_tasks"
  run_chat_case "${tiny_bin}" "chat_session_save_cmd" "restore-case" "4096" $'/remember project alias atlas-9\n/session save\n' "restore-seq"
  run_chat_case "${tiny_bin}" "chat_session_restore_cmd" "restore-case" "4096" $'/session restore\n/memory show\n' "restore-seq" "no"
  run_chat_case "${tiny_bin}" "chat_session_list_cmd" "restore-case" "4096" $'/session list\n' "restore-seq" "no"

  write_summary
  echo "summary: ${OUTDIR}/summary.json"
}

main "$@"
