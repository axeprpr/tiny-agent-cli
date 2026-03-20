# onek-agent

`onek-agent` 是一个偏实用主义的轻量终端 agent。

你可以把它理解成：

- 一个丐版的 `claude-cli`
- 一个轻量化的 `codex`
- 一个只靠单文件二进制和 OpenAI 兼容接口就能跑起来的 coding agent

它追求的是“小、够用、可控”，不是全家桶。

英文版见 [README.md](/root/1k-install/README.md)。

## 它能做什么

- 单次执行任务
- 多轮等待式交互
- 读写文件
- grep 搜索
- 执行 shell 命令
- 抓网页和简单 web search
- 接本地模型或兼容 OpenAI API 的服务

## 核心思路

- `PDCA`
  内置提示词会推动模型先计划、再执行、再检查、再调整。
- `ReAct`
  先观察，再决定动作，再调用工具，再根据结果修正。
- `默认安全`
  shell 命令默认要确认。
- `dangerously 模式`
  你需要速度时，可以整场会话跳过确认。
- `默认 raw 输出`
  不强行过度改写模型原生回答。
- `terminal 输出模式`
  需要时再做轻量终端格式整理。

## 命令

- `onek run [flags] <task>`
  跑一次任务
- `onek chat [flags]`
  进入等待模式，可以连续提问
- `onek models`
  查看模型列表
- `onek ping`
  测试接口和模型
- `onek version`
  查看版本

## 命令确认与 dangerously

- `confirm`
  默认模式。每次执行 shell 命令前都会确认。
- `dangerously`
  本次运行或本次会话里，跳过命令确认。

示例：

```bash
onek run --approval confirm "检查这个仓库"
onek run --dangerously "运行 go test ./..."
onek chat --dangerously
```

## 一句话运行

把下面命令里的接口地址和模型名换成你自己的即可。

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

你的接口示例：

```bash
curl -L https://github.com/axeprpr/onek-agent/releases/latest/download/onek-linux-amd64 -o ./onek && chmod +x ./onek && MODEL_BASE_URL='https://llm.haohuapm.com:20020' MODEL_NAME='Qwen3.5-27B-FP8' ./onek chat
```

## 等待模式

`chat` 会保留上下文，所以它已经不只是“一次一问”的工具，而是一个轻量的持续会话 agent。

内置控制命令：

- `/help`
- `/reset`
- `/exit`

示例：

```text
onek> 看看这个项目是干什么的
onek> 接下来优先改什么
onek> /reset
onek> 给我写个最小发布检查单
```

## 输出模式

- `--output raw`
  默认值。尽量保留模型原生输出。
- `--output terminal`
  只做轻量终端优化，主要用于减少 Markdown 表格在终端里的混乱感。

示例：

```bash
onek run --output raw "帮我统计环境信息"
onek run --output terminal "帮我统计环境信息"
```

## 环境变量

- `MODEL_BASE_URL`
  默认值：`http://127.0.0.1:11434/v1`
- `MODEL_NAME`
  默认值：`qwen2.5-coder:7b`
- `MODEL_API_KEY`
  默认值：空
- `AGENT_WORKDIR`
  默认值：当前目录
- `AGENT_MAX_STEPS`
  默认值：`8`
- `AGENT_COMMAND_TIMEOUT`
  默认值：`30s`
- `AGENT_SHELL`
  默认值：Linux/macOS 下为 `bash`，Windows 下为 `powershell.exe`
- `AGENT_APPROVAL`
  默认值：`confirm`

## 源码构建

```bash
go test ./...
go build ./...
go run ./cmd/onek version
```

## 发布

本地构建：

```bash
./scripts/build-release.sh v0.1.2
```

GitHub 自动发版：

- 推一个 tag，比如 `v0.1.2`
- GitHub Actions 会自动构建 `linux`、`darwin`、`windows`
- 上传资产是原始二进制，不再额外压缩

## 状态

它现在已经不是玩具了，但仍然刻意保持轻量。

如果你要的是一个便宜、可改、终端原生的 coding agent，这个方向是对的。
