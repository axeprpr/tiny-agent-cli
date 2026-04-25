# tacli

`tacli` 是一个尽量收敛复杂度的代码 Agent：一个二进制、一个工作区、一个 OpenAI 兼容模型接口。

它的运行面很小：

- 只有 Go 二进制
- 本地状态统一放在 `.tacli/`
- 内置工具运行时，带权限、Hook、审计和后台任务
- 同时支持终端聊天、单次任务执行和轻量 Web dashboard

English version: [README.md](README.md)

## 功能

- 终端优先：`chat`、`run`、`status`、`plan`、`contract`、后台任务都在一个二进制里
- Web dashboard：浏览器聊天界面，支持流式输出、审批条、工具卡片、生成文件卡片和会话状态
- 文件检查：内置 DOCX/PDF 检查工具、文本查看、下载，以及对生成网页的沙箱 HTML 预览
- 安全执行控制：权限模式、命令规则、审批流、审计日志和 Hook 集成
- 本地持久化：会话、transcript、memory、task contract、todo、trace、audit 都落在 `.tacli/`
- 简单部署：一个 OpenAI 兼容接口、一个工作区根目录、一个本地状态目录

## 架构

### 1. 运行时总览

```mermaid
flowchart LR
    user[用户]

    subgraph entry[入口层]
        cli["cmd/tacli/main.go<br/>run / chat / status / init / models / version"]
        tui["cmd/tacli/chat_tui.go<br/>交互式终端 UI"]
        dashboard["cmd/tacli/dashboard.go<br/>Web dashboard + SSE 状态流"]
        control["cmd/tacli/control.go<br/>plan / status / contract / skills / capabilities"]
    end

    subgraph runtime[运行时装配]
        chatrt["chatRuntime<br/>会话路径、记忆作用域、后台任务、插件、审批"]
        factory["internal/harness/factory.go<br/>构造 Prompt 上下文并装配依赖"]
        prompt["PromptContext<br/>instructions + skills + capabilities + git + memory"]
    end

    subgraph core[Agent 核心]
        agent["internal/agent<br/>会话循环、重试、压缩、finish gate"]
        model["internal/model/openaiapi<br/>OpenAI 兼容 HTTP/流式客户端"]
        registry["internal/tools/registry.go<br/>工具定义与执行管线"]
    end

    subgraph tooling[工具运行时]
        permission["权限策略<br/>工具级策略 + 命令规则"]
        hooks["Hook Runner<br/>前后置 Hook"]
        audit["审计 + dashboard 工具事件流"]
        tools["内置工具<br/>todo / contract / files / edit / shell / web / 文档检查 / MCP / bg jobs"]
    end

    subgraph state[持久化]
        session["internal/session<br/>会话与 transcript"]
        memory["internal/memory"]
        tasks["internal/tasks + task contract + todo"]
        trace["trace + audit 日志"]
        disk["workspace + .tacli/"]
    end

    subgraph webui[Dashboard UI]
        sse["SSE state/events"]
        approvals["审批条"]
        files["生成文件卡片"]
        preview["沙箱 HTML 预览"]
    end

    user --> cli
    user --> tui
    user --> dashboard
    cli --> control
    cli --> chatrt
    tui --> chatrt
    dashboard --> chatrt
    dashboard --> sse
    sse --> approvals
    sse --> files
    files --> preview
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

### 2. 单轮执行流程

```mermaid
sequenceDiagram
    participant User as 用户
    participant UI as CLI / TUI / Dashboard
    participant Runtime as chatRuntime
    participant Factory as harness.Factory
    participant Session as agent.Session
    participant Model as model client
    participant Registry as tool registry
    participant Tool as concrete tool

    User->>UI: 提交任务
    UI->>Runtime: 发起任务 / 聊天轮次
    Runtime->>Factory: 构造 PromptContext
    Factory->>Session: NewSessionWithPrompt(...)
    Session->>Model: chat/completions
    Model-->>Session: assistant 回复或 tool calls

    alt assistant 请求工具
        loop 每个工具调用
            Session->>Registry: Call(name, args)
            Registry->>Registry: 校验 schema
            Registry->>Registry: 权限判定
            Registry->>Registry: 运行 pre-hook
            Registry->>Tool: 执行
            Tool-->>Registry: 输出 / 错误
            Registry->>Registry: 运行 post-hook + audit
            Registry-->>Session: tool result message
        end
        Session->>Model: 带着工具结果继续请求
        Model-->>Session: 下一轮回复
    end

    Session-->>Runtime: 最终答案 + turn summary
    Runtime-->>UI: 流式输出 / 渲染
```

### 3. Dashboard 文件与预览流

```mermaid
flowchart LR
    user[浏览器用户]
    dashboard[dashboard UI]
    runtime[chatRuntime]
    audit[工具审计事件]
    files[生成文件卡片]
    preview[/api/preview/...]
    iframe[sandbox iframe]
    workspace[工作区文件]

    user --> dashboard
    dashboard --> runtime
    runtime --> audit
    audit --> files
    runtime --> workspace
    files --> preview
    preview --> workspace
    preview --> iframe
```

### 4. 状态与文件布局

```mermaid
flowchart TB
    subgraph repo[工作区根目录]
        claw["CLAW.md / .claw/<br/>仓库指令"]
        roadmap["plan.md<br/>供 tacli plan 读取"]
        work["源码树"]
        state[".tacli/"]
    end

    subgraph files[.tacli 内容]
        sessions["sessions/*.json<br/>持久化会话"]
        transcripts["transcripts/*.log<br/>文本 transcript"]
        perms["permissions.json<br/>工具与命令策略"]
        memory["memory.json<br/>global/team/project memory"]
        todo["tasks-v2.json<br/>todo 列表"]
        contract["contract-v1.json<br/>task contract"]
        audit["audit.jsonl<br/>工具事件"]
        trace["trace.jsonl<br/>运行时事件"]
    end

    subgraph live[运行时读写者]
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

## 仓库结构

| 路径 | 作用 |
| --- | --- |
| `cmd/tacli/` | CLI 入口、交互式聊天运行时、TUI、slash command 对齐、后台任务管理 |
| `cmd/tacli/dashboard.go` + `cmd/tacli/dashboard_assets/` | 浏览器 dashboard、SSE 状态流、审批、工具卡片、文件预览与下载 |
| `internal/harness/` | 依赖装配、Prompt 上下文构造、模型/Agent/工具初始化 |
| `internal/agent/` | 会话循环、turn summary、重试、上下文压缩、finish gate、编排 |
| `internal/tools/` | 工具注册表、权限层、Hook、审计、task contract、文件/命令/Web/文档检查/MCP 工具 |
| `internal/model/openaiapi/` | OpenAI 兼容模型客户端与流式传输 |
| `internal/session/` | 会话和 transcript 持久化 |
| `internal/memory/` | 持久化记忆 |
| `internal/tasks/` | CLI 控制面使用的轻量任务记录 |
| `release-site/` | 静态发布页 |
| `scripts/` | 发布、安装、回归、对比脚本 |

## 使用

### 1. 构建

```bash
go build -o tacli ./cmd/tacli
```

### 2. 配置模型接口

```bash
export MODEL_BASE_URL="https://api.openai.com/v1"
export MODEL_NAME="gpt-5-mini"
export MODEL_API_KEY="your-api-key"
```

常用环境变量：

- `AGENT_APPROVAL=confirm|dangerously`
- `AGENT_WORKDIR=/path/to/repo`
- `AGENT_STATE_DIR=/path/to/.tacli`
- `MODEL_CONTEXT_WINDOW=...`

### 3. 运行

```bash
tacli ping
tacli chat
```

单次任务：

```bash
tacli run "inspect this repository and summarize the architecture"
```

本地信任模式：

```bash
tacli chat --dangerously
tacli run --dangerously "go test ./..."
```

Web dashboard：

```bash
tacli dashboard --host 127.0.0.1 --port 8421
```

Dashboard 当前支持：

- SSE 流式会话状态
- 命令/写文件审批条
- 工具调用时间线与输入输出摘要
- 生成文件卡片里的 `View`、`Download`、`.html` 的 `Preview`
- 通过 `/api/preview/...` 做沙箱 HTML 预览

### 4. 常用命令

```bash
tacli status
tacli models
tacli version
tacli plan
tacli contract
```

聊天内常用 slash commands：

```text
/status
/plan
/contract
/skills
/capabilities
/policy ...
/bg ...
```

## 说明

- 默认本地状态目录是 `.tacli/`
- `tacli plan` 会读取工作区根目录的 `plan.md`
- 后台任务需要 `--dangerously`，因为它们不能在中途等待交互式审批
- dashboard 的 HTML 预览是沙箱化的，只允许一小部分静态资源扩展名
- malformed PDF 现在会作为普通工具错误返回，不会再把 dashboard 进程打崩
