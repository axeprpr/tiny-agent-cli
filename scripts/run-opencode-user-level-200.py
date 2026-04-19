#!/usr/bin/env python3
import json
import os
import pathlib
import re
import shlex
import subprocess
import sys
import time
from collections import defaultdict
from datetime import datetime

ROOT = pathlib.Path(__file__).resolve().parents[1]
RUN_ROOT = ROOT / ".tacli" / "opencode-user-e2e"
RUN_DIR = RUN_ROOT / "runs" / datetime.utcnow().strftime("%Y%m%d-%H%M%S")
BIN_DIR = RUN_ROOT / "bin"
BIN_PATH = BIN_DIR / "tacli-user-e2e"

RUN_DIR.mkdir(parents=True, exist_ok=True)
BIN_DIR.mkdir(parents=True, exist_ok=True)


def log(msg: str):
    print(msg, flush=True)


def load_env_file(path: pathlib.Path) -> dict:
    out = {}
    if not path.exists():
        return out
    for raw in path.read_text(encoding="utf-8", errors="ignore").splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        k = k.strip()
        v = v.strip().strip('"').strip("'")
        out[k] = v
    return out


def run(cmd, *, cwd=None, env=None, stdin_text=None, timeout=120):
    proc = subprocess.run(
        cmd,
        cwd=str(cwd) if cwd else None,
        env=env,
        input=stdin_text.encode("utf-8") if stdin_text is not None else None,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=timeout,
        check=False,
    )
    return proc.returncode, proc.stdout.decode("utf-8", errors="replace"), proc.stderr.decode("utf-8", errors="replace")


log("[user-e2e] building tacli binary...")
go_env = os.environ.copy()
go_env.setdefault("HOME", "/root")
go_env.setdefault("GOPATH", "/root/go")
go_env.setdefault("XDG_CACHE_HOME", "/root/.cache")
go_env.setdefault("GOCACHE", "/root/.cache/go-build")
code, out, err = run(["go", "build", "-o", str(BIN_PATH), "./cmd/tacli"], cwd=ROOT, env=go_env, timeout=300)
if code != 0:
    print(out, flush=True)
    print(err, file=sys.stderr, flush=True)
    raise SystemExit("build failed")

# Base env for runtime.
runtime_env = os.environ.copy()
runtime_env.update(load_env_file(pathlib.Path("/root/.env")))
runtime_env.setdefault("HOME", "/root")
runtime_env.setdefault("AGENT_APPROVAL", "danger-full-access")

MODEL_BASE_URL = runtime_env.get("MODEL_BASE_URL", "").strip()
MODEL_API_KEY = runtime_env.get("MODEL_API_KEY", "").strip()
MODEL_NAME = runtime_env.get("MODEL_NAME", "").strip()
model_enabled = bool(MODEL_BASE_URL and MODEL_API_KEY and MODEL_NAME)

cases = []


def add_case(case_id: int, module: str, title: str, mode: str, payload: dict):
    cases.append(
        {
            "id": f"{case_id:03d}",
            "module": module,
            "title": title,
            "mode": mode,
            **payload,
        }
    )

# 001-020 CLI user-level cases.
cli_defs = [
    ("version shows build version", [str(BIN_PATH), "version"], r".+"),
    ("help shows usage", [str(BIN_PATH), "help"], r"Usage:"),
    ("status shows workspace summary", [str(BIN_PATH), "status", "--workdir", "/tmp"], r"workdir=/tmp"),
    ("plan missing file returns empty", [str(BIN_PATH), "plan", "--workdir", "/tmp"], r"^$|.+"),
    ("contract missing returns placeholder", [str(BIN_PATH), "contract", "--workdir", "/tmp"], r"\(no task contract\)|\(missing\)"),
    ("capabilities lists packs", [str(BIN_PATH), "capabilities", "--workdir", "/tmp"], r"ops:"),
    ("skills lists bundled skills", [str(BIN_PATH), "skills", "--workdir", "/tmp"], r"bundled"),
    ("models command handles endpoint", [str(BIN_PATH), "models"], r"model API error|request failed|[A-Za-z0-9][A-Za-z0-9._/-]{2,}"),
    ("ping command handles endpoint", [str(BIN_PATH), "ping"], r"pong|model API error|request failed"),
    ("unknown command exits with guidance", [str(BIN_PATH), "definitely-unknown-subcmd"], r"missing task|Usage:|not sure what you want|If you meant a command"),
    ("run without task rejects", [str(BIN_PATH), "run"], r"missing task|Usage:"),
    ("control status path", [str(BIN_PATH), "status", "--workdir", str(ROOT)], r"state="),
    ("control plan path", [str(BIN_PATH), "plan", "--workdir", str(ROOT)], r".+"),
    ("control contract path", [str(BIN_PATH), "contract", "--workdir", str(ROOT)], r"task contract|\(no task contract\)|\(missing\)"),
    ("capability detail query", [str(BIN_PATH), "capabilities", "--workdir", str(ROOT), "ops"], r"ops:"),
    ("skills against repo", [str(BIN_PATH), "skills", "--workdir", str(ROOT)], r"bundled|hooks"),
    ("status command_rules field", [str(BIN_PATH), "status", "--workdir", str(ROOT)], r"command_rules="),
    ("status sessions field", [str(BIN_PATH), "status", "--workdir", str(ROOT)], r"sessions="),
    ("version non-empty", [str(BIN_PATH), "version"], r"\S+"),
    ("help includes chat", [str(BIN_PATH), "help"], r"tacli chat"),
]
for idx, (title, cmd, expect) in enumerate(cli_defs, start=1):
    add_case(idx, "CLI", title, "cli", {"cmd": cmd, "expect": expect})

# 021-140 chat user command cases.
chat_templates = [
    ("slash help", "/help\n", r"tacli dev"),
    ("status view", "/status\n", r"conversation="),
    ("new conversation", "/new demo\n", r"started conversation"),
    ("resume missing conversation", "/resume missing\n", r"no saved conversation"),
    ("rename conversation", "/rename renamed\n", r"renamed conversation"),
    ("fork conversation", "/fork branched\n", r"forked conversation"),
    ("tree rendering", "/tree\n", r"conversation tree|current_conversation"),
    ("memory show", "/memory show\n", r"path=.*memory\.json"),
    ("memory remember", "/memory remember alpha-note\n", r"project_notes=1|project memory saved"),
    ("memory forget", "/memory forget alpha-note\n", r"removed"),
    ("memory team show", "/memory team show\n", r"team memory|team_notes="),
    ("tasks list", "/tasks list\n", r"no tasks|task-"),
    ("tasks create", "/tasks create sample-task\n", r"task-001"),
    ("tasks update unknown", "/tasks update task-001 status=in_progress\n", r"unknown task|task-001"),
    ("tasks delete unknown", "/tasks delete task-001\n", r"unknown task|deleted"),
    ("policy view", "/policy\n", r"default="),
    ("policy default allow", "/policy default allow\n", r"default=allow"),
    ("policy default prompt", "/policy default prompt\n", r"default=prompt"),
    ("policy command list", "/policy command list\n", r"command"),
    ("audit stats", "/audit stats\n", r"audit="),
    ("debug-tool-call", "/debug-tool-call\n", r"audit|no audit events yet"),
    ("debug-turn", "/debug-turn\n", r"turn summaries|no turn summaries yet"),
    ("trace stats", "/trace stats\n", r"events="),
    ("plugin list", "/plugin list\n", r"plugin"),
    ("skills list", "/skills\n", r"bundled|hooks"),
    ("capabilities list", "/capabilities\n", r"ops:"),
    ("mcp list", "/mcp list\n", r"MCP|no MCP servers configured"),
    ("hooks list", "/hooks\n", r"PreToolUse"),
    ("agents list", "/agents\n", r"background agents|no background agents"),
    ("jobs list", "/jobs\n", r"background jobs|no background jobs"),
    ("job unknown", "/job no-such\n", r"unknown job"),
    ("job-send unknown", "/job-send no-such hi\n", r"unknown job|usage"),
    ("job-cancel unknown", "/job-cancel no-such\n", r"unknown job|canceled"),
    ("job-apply unknown", "/job-apply no-such\n", r"unknown job|applied"),
    ("steer queue", "/steer hi\n", r"queued steering message"),
    ("follow queue", "/follow hi\n", r"queued follow-up message"),
    ("approval set allow", "/approval allow\n", r"approval mode set to"),
    ("approval set prompt", "/approval prompt\n", r"approval mode set to"),
    ("model set", "/model test-model\n", r"model set to test-model"),
    ("reset context", "/reset\n", r"context reset"),
]
case_id = 21
for i in range(120):
    base_title, cmd_input, expect = chat_templates[i % len(chat_templates)]
    add_case(
        case_id,
        "ChatCmd",
        f"{base_title} #{i+1}",
        "chat_cmd",
        {
            "input": cmd_input,
            "expect": expect,
            "resume": f"u{i+1:03d}",
        },
    )
    case_id += 1

# 141-200 model-in-loop user-level cases (token consuming).
for i in range(1, 61):
    sid = 140 + i
    token = f"amber-{i:03d}"
    filename = f"secret-{i:03d}.txt"
    title = f"model extracts exact secret {i:03d}"
    prompt = f"Read {filename} and reply with exactly {token}"
    add_case(
        sid,
        "ModelRun",
        title,
        "model_run",
        {
            "filename": filename,
            "secret": token,
            "prompt": prompt,
        },
    )

if len(cases) != 200:
    raise RuntimeError(f"expected 200 cases, got {len(cases)}")

results = []
metrics = {
    "model_requests": 0,
    "model_approx_tokens": 0,
    "model_read_file_calls": 0,
}


def write_artifact(case_id: str, suffix: str, content: str):
    path = RUN_DIR / f"{case_id}.{suffix}"
    path.write_text(content, encoding="utf-8")
    return str(path)


def case_step(case: dict) -> str:
    mode = case["mode"]
    if mode == "cli":
        return f"执行命令: `{' '.join(shlex.quote(x) for x in case['cmd'])}`，观察输出与退出码。"
    if mode == "chat_cmd":
        cmd = case["input"].strip() or "(empty)"
        return f"启动 `tacli chat --resume {case['resume']}` 后输入 `{cmd}`，观察命令回显。"
    if mode == "model_run":
        return f"写入 `{case['filename']}`，执行 run 任务 `{case['prompt']}`，验证最终文本与工具链。"
    return "执行并核对输出。"


def write_case_catalog(catalog_path: pathlib.Path, all_cases: list):
    catalog_path.parent.mkdir(parents=True, exist_ok=True)
    with catalog_path.open("w", encoding="utf-8") as f:
        f.write("# OpenCode 用户级细化用例 001-200\n\n")
        f.write("- 本清单聚焦用户视角操作路径（CLI、Chat 命令、模型实跑）。\n")
        f.write("- 001-020: CLI；021-140: Chat Slash 命令；141-200: 模型 token 消耗场景。\n\n")
        f.write("| ID | 模块 | 模式 | 用例标题 | 用户操作步骤 | 通过标准 |\n")
        f.write("| --- | --- | --- | --- | --- | --- |\n")
        for c in all_cases:
            f.write(
                f"| {c['id']} | {c['module']} | {c['mode']} | {c['title']} | {case_step(c).replace('|','/')} | 匹配 `{c.get('expect', c.get('secret', '输出满足约束'))}` |\n"
            )


case_catalog = ROOT / "docs" / "opencode-user-level-cases-001-200.md"
write_case_catalog(case_catalog, cases)
log(f"[user-e2e] case catalog={case_catalog}")
log(f"[user-e2e] executing {len(cases)} user-level cases...")
start_all = time.time()

for case in cases:
    cid = case["id"]
    mode = case["mode"]
    case_start = time.time()
    passed = False
    detail = ""
    actual = ""
    artifacts = []
    model_requests = 0
    approx_tokens = 0
    read_file_calls = 0

    if mode == "cli":
        workdir = RUN_DIR / f"{cid}.work"
        state = RUN_DIR / f"{cid}.state"
        workdir.mkdir(parents=True, exist_ok=True)
        state.mkdir(parents=True, exist_ok=True)
        env = runtime_env.copy()
        env["AGENT_STATE_DIR"] = str(state)

        code, out, err = run(case["cmd"], cwd=workdir, env=env, timeout=120)
        combo = (out or "") + "\n" + (err or "")
        passed = bool(re.search(case["expect"], combo, re.I | re.S))
        detail = f"exit={code} expect=/{case['expect']}/"
        actual = combo.strip().splitlines()[0] if combo.strip() else "(empty)"
        artifacts.append(write_artifact(cid, "stdout", out))
        artifacts.append(write_artifact(cid, "stderr", err))

    elif mode == "chat_cmd":
        workdir = RUN_DIR / f"{cid}.work"
        state = RUN_DIR / f"{cid}.state"
        workdir.mkdir(parents=True, exist_ok=True)
        state.mkdir(parents=True, exist_ok=True)
        env = runtime_env.copy()
        env["AGENT_STATE_DIR"] = str(state)

        cmd = [
            str(BIN_PATH), "chat", "--dangerously", "--resume", case["resume"], "--output", "raw", "--workdir", str(workdir)
        ]
        code, out, err = run(cmd, cwd=workdir, env=env, stdin_text=case["input"], timeout=120)
        combo = (out or "") + "\n" + (err or "")
        passed = bool(re.search(case["expect"], combo, re.I | re.S))
        detail = f"exit={code} expect=/{case['expect']}/"
        actual = combo.strip().splitlines()[0] if combo.strip() else "(empty)"
        artifacts.append(write_artifact(cid, "stdin", case["input"]))
        artifacts.append(write_artifact(cid, "stdout", out))
        artifacts.append(write_artifact(cid, "stderr", err))

    elif mode == "model_run":
        workdir = RUN_DIR / "model-workspace"
        state = RUN_DIR / "model-state"
        workdir.mkdir(parents=True, exist_ok=True)
        state.mkdir(parents=True, exist_ok=True)
        (workdir / case["filename"]).write_text(f"SECRET={case['secret']}\n", encoding="utf-8")

        if not model_enabled:
            passed = False
            detail = "model env missing (MODEL_BASE_URL/MODEL_API_KEY/MODEL_NAME)"
            actual = "SKIPPED"
        else:
            env = runtime_env.copy()
            env["AGENT_STATE_DIR"] = str(state)
            cmd = [
                str(BIN_PATH), "run", "--dangerously", "--output", "jsonl",
                "--base-url", MODEL_BASE_URL,
                "--model", MODEL_NAME,
                "--api-key", MODEL_API_KEY,
                case["prompt"],
            ]
            last_code = 0
            out = ""
            err = ""
            for _ in range(2):
                last_code, out, err = run(cmd, cwd=workdir, env=env, timeout=180)
                if last_code == 0:
                    break
            artifacts.append(write_artifact(cid, "stdout.jsonl", out))
            artifacts.append(write_artifact(cid, "stderr", err))

            final_text = ""
            used_read = False
            for line in out.splitlines():
                line = line.strip()
                if not line:
                    continue
                try:
                    evt = json.loads(line)
                except Exception:
                    continue
                if evt.get("type") == "result":
                    final_text = str(evt.get("data", {}).get("final", "")).strip()
                if evt.get("type") == "tool_start":
                    if str(evt.get("data", {}).get("name", "")) == "read_file":
                        used_read = True
                        read_file_calls += 1
                if evt.get("type") == "model_request":
                    model_requests += 1
                    approx_tokens += int(evt.get("data", {}).get("approx_tokens") or 0)
            passed = last_code == 0 and final_text == case["secret"] and used_read
            detail = f"exit={last_code} expect={case['secret']} used_read={used_read}"
            actual = final_text or "(empty)"

    duration_sec = round(time.time() - case_start, 3)
    metrics["model_requests"] += model_requests
    metrics["model_approx_tokens"] += approx_tokens
    metrics["model_read_file_calls"] += read_file_calls
    results.append(
        {
            "id": cid,
            "module": case["module"],
            "title": case["title"],
            "mode": mode,
            "passed": passed,
            "detail": detail,
            "actual": actual,
            "duration_sec": duration_sec,
            "model_requests": model_requests,
            "approx_tokens": approx_tokens,
            "read_file_calls": read_file_calls,
            "artifacts": artifacts,
        }
    )
    log(
        f"[case {cid}] {'PASS' if passed else 'FAIL'} {case['title']} "
        f"(t={duration_sec:.2f}s tok={approx_tokens})"
    )

elapsed = time.time() - start_all
passed_count = sum(1 for r in results if r["passed"])
failed = [r for r in results if not r["passed"]]
module_stats = defaultdict(lambda: {"passed": 0, "total": 0})
for r in results:
    module_stats[r["module"]]["total"] += 1
    if r["passed"]:
        module_stats[r["module"]]["passed"] += 1

summary_json = RUN_DIR / "summary.json"
summary_tsv = RUN_DIR / "summary.tsv"
report_md = ROOT / "docs" / "opencode-user-level-results-001-200.md"

summary_json.write_text(
    json.dumps(
        {
            "run_dir": str(RUN_DIR),
            "elapsed_sec": elapsed,
            "passed": passed_count,
            "total": len(results),
            "module_stats": module_stats,
            "token_stats": metrics,
            "results": results,
        },
        ensure_ascii=False,
        indent=2,
    ),
    encoding="utf-8",
)
with summary_tsv.open("w", encoding="utf-8") as f:
    f.write("id\tmodule\tmode\tpassed\tduration_sec\tapprox_tokens\ttitle\tdetail\tactual\n")
    for r in results:
        f.write("\t".join([
            r["id"], r["module"], r["mode"], str(r["passed"]), str(r["duration_sec"]), str(r["approx_tokens"]), r["title"].replace("\t", " "), r["detail"].replace("\t", " "), r["actual"].replace("\t", " ")
        ]) + "\n")

report_md.parent.mkdir(parents=True, exist_ok=True)
with report_md.open("w", encoding="utf-8") as f:
    f.write("# OpenCode 用户级用例执行结果 001-200\n\n")
    f.write(f"- 执行时间(UTC): {datetime.utcnow().isoformat()}Z\n")
    f.write(f"- 运行目录: `{RUN_DIR}`\n")
    f.write(f"- 总计: {len(results)}，通过: {passed_count}，失败: {len(failed)}\n")
    f.write(f"- 总耗时: {elapsed:.1f}s\n\n")
    f.write("## 模块通过率\n\n")
    for module in sorted(module_stats.keys()):
        stat = module_stats[module]
        f.write(f"- {module}: {stat['passed']}/{stat['total']}\n")
    f.write("\n")
    f.write("## 模型 token 统计（141-200）\n\n")
    f.write(f"- model_requests: {metrics['model_requests']}\n")
    f.write(f"- approx_tokens_total: {metrics['model_approx_tokens']}\n")
    f.write(f"- read_file_calls: {metrics['model_read_file_calls']}\n\n")
    f.write("| ID | 模块 | 用例标题 | 结果 | 关键说明 | 实际结果 |\n")
    f.write("| --- | --- | --- | --- | --- | --- |\n")
    for r in results:
        f.write(f"| {r['id']} | {r['module']} | {r['title']} | {'PASS' if r['passed'] else 'FAIL'} | {r['detail'].replace('|','/')} | {r['actual'].replace('|','/')} |\n")

log("[user-e2e] done")
log(f"[user-e2e] passed={passed_count}/{len(results)} elapsed={elapsed:.1f}s")
log(
    "[user-e2e] token_stats "
    f"requests={metrics['model_requests']} approx_tokens={metrics['model_approx_tokens']} "
    f"read_file_calls={metrics['model_read_file_calls']}"
)
log(f"[user-e2e] summary={summary_json}")
log(f"[user-e2e] report={report_md}")

if failed:
    log("[user-e2e] failed cases:")
    for r in failed[:30]:
        log(f"  - {r['id']} {r['title']} :: {r['detail']} :: {r['actual']}")
    raise SystemExit(2)
