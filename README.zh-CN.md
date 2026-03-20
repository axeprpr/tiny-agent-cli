# tiny-agent-cli

`tiny-agent-cli` 是一个为离线环境、弱网环境、低依赖环境准备的轻量终端 agent。

你可以把它理解成：

- 一个丐版的 `claude-cli`
- 一个轻量化的 `codex`
- 一个只靠单文件二进制和 OpenAI 兼容接口就能跑起来的 coding agent

它的定位非常直接：

- 单文件二进制
- 不需要安装 Node.js
- 不需要 Python 运行时
- 不需要 Electron
- 不需要常驻后台服务
- 支持 Linux、macOS、Windows 全端运行
- 只依赖一个 OpenAI 兼容 LLM 接口，也可以接你自己的本地模型服务

它追求的是“小、够用、可控”，不是全家桶。

英文版见 [README.md](README.md)。

## 它为什么存在

很多 agent CLI 很强，但也很重：

- 要装 Node.js
- 要装一堆额外依赖
- 初始化链路长
- 在离线机、服务器、容器、救援环境里不够顺手

`tiny-agent-cli` 走的是反方向：

- 一个二进制
- 一个模型接口
- 一个工作目录
- 不折腾环境

它不是想做最大最全的 agent 平台，而是想做你可以随手丢到任何机器上就开干的那个“丐版 codex”。

## 它能做什么

- 单次执行任务
- 多轮等待式交互
- 全局记忆 + 项目记忆
- 读写文件
- grep 搜索
- 执行 shell 命令
- 抓网页和简单 web search
- 接本地模型或兼容 OpenAI API 的服务
- 直接下载原始二进制运行，不需要 zip/tgz 解压流程

## 核心思路

- `PDCA`
  内置提示词会推动模型先计划、再执行、再检查、再调整。
- `ReAct`
  先观察，再决定动作，再调用工具，再根据结果修正。
- `默认安全`
  shell 命令默认要确认。
- `分层记忆`
  全局偏好和项目规则分开存，不会把所有上下文都塞进每次会话。
- `dangerously 模式`
  你需要速度时，可以整场会话跳过确认。
- `默认 raw 输出`
  不强行过度改写模型原生回答。
- `terminal 输出模式`
  `chat` 默认走终端友好渲染，`run` 默认保留原生输出。

## 命令

- `tacli`
  交互终端里默认直接进入 chat
- `tacli -d`
  直接进入 dangerously 模式的 chat
- `tacli run [--dangerously] <task>`
  跑一次任务
- `tacli <task>`
  一次性任务的简写
- `tacli chat`
  进入等待模式，可以连续提问；在交互终端里会默认进入全屏 TUI
- `tacli models`
  查看模型列表
- `tacli ping`
  测试接口和模型
- `tacli version`
  查看版本

## 命令确认与 dangerously

- `confirm`
  默认模式。执行 shell 命令和写文件前都会确认。
- `dangerously`
  本次运行或本次会话里，跳过 shell 和写文件确认。

示例：

```bash
tacli
tacli -d
tacli "检查这个仓库"
tacli -d "运行 go test ./..."
tacli run --dangerously "运行 go test ./..."
tacli chat --dangerously
```

## 一句话安装

安装脚本会自动识别架构，所以每个平台只保留一条推荐命令。

Linux 或 macOS：

```bash
curl -fsSL https://gh-proxy.com/https://raw.githubusercontent.com/axeprpr/tiny-agent-cli/main/scripts/install.sh | bash && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='your-model' ~/.local/bin/tacli
```

Windows PowerShell：

```powershell
iwr https://gh-proxy.com/https://raw.githubusercontent.com/axeprpr/tiny-agent-cli/main/scripts/install.ps1 -UseBasicParsing | iex; $env:MODEL_BASE_URL='http://127.0.0.1:11434/v1'; $env:MODEL_NAME='your-model'; $HOME\.local\bin\tacli.exe
```

可选安装变量：

- `TACLI_VERSION`
  安装指定版本，比如 `v0.1.2`
- `TACLI_INSTALL_DIR`
  安装到自定义目录

## 等待模式

`chat` 会保留上下文，所以它已经不只是“一次一问”的工具，而是一个轻量的持续会话 agent。

在交互终端里，`chat` 现在会直接打开全屏 TUI，包含：

- 顶部信息条，显示工作目录、shell、模型、审批模式
- 单栏消息主视图，执行步骤和工具活动会直接并入对话
- 可折叠的活动抽屉，可按步骤、工具、错误、审批过滤
- 紧凑的单行输入区，带动态提示
- 对回答里的 Markdown / 代码块做更好的终端渲染
- 底部状态栏
- 模型、审批模式、会话名、上下文剩余估算
- 命令 / 文件写入审批直接并入对话流
- `Ctrl+O` 可展开或收起活动抽屉
- `Ctrl+G` 可切换活动日志过滤器
- `F1` 可展开或收起帮助

内置控制命令：

- `/help`
- `/session [name|new]`
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

示例：

```text
tacli> 看看这个项目是干什么的
tacli> 接下来优先改什么
tacli> /approval dangerously
tacli> /remember 默认输出中文，简洁回答
tacli> /remember-global 优先用简短中文回答
tacli> /memorize
tacli> /output terminal
tacli> /reset
tacli> 给我写个最小发布检查单
```

## 会话持久化

`chat` 默认会把会话保存在 `.tacli` 目录下。

- 会话状态：
  `.tacli/sessions/<session>.json`
- transcript 日志：
  `.tacli/transcripts/<session>.log`

默认每次启动 `chat` 都会新建一个带时间戳的会话，并在退出时自动把稳定上下文整理进记忆。需要恢复或切换会话时，可以用 `tacli chat --session <name>` 或 `/session <name>`。
如果要改存储位置，继续用环境变量比如 `AGENT_STATE_DIR` 就可以。

## 持久化记忆

现在 `tiny-agent-cli` 已经支持一个轻量的长期记忆层。

它分成两个作用域：

- 全局记忆
  跨项目通用的偏好，比如语言、回答风格
- 项目记忆
  只绑定当前工作目录的规则和背景

你可以把稳定偏好、项目规则、长期背景信息记进去：

```text
tacli> /remember-global 默认输出中文，简洁回答。
tacli> /remember 这个项目优先支持 ARM64。
tacli> /scope
tacli> /memory
tacli> /memorize
tacli> /forget ARM64
```

记忆文件默认保存在：

- `.tacli/memory.json`

后续新的 chat 会话会把命中的记忆作为背景上下文注入到第一条 system prompt。

补充说明：

- 项目记忆按工作目录路径隔离
- `/memorize` 会把当前会话提炼成项目记忆
- chat 会在退出时自动执行这一步
- 如果模型侧的记忆总结超时或失败，`tiny-agent-cli` 会回退到本地提取明显的长期偏好和项目事实
- 长会话会被压缩成一段本地摘要，同时尽量保留最近几轮完整上下文，更适合短上下文模型

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
go run ./cmd/tacli version
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
