# onek-agent

`onek-agent` 是一个为离线环境、弱网环境、低依赖环境准备的轻量终端 agent。

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

`onek-agent` 走的是反方向：

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
  需要时再做轻量终端格式整理。

## 命令

- `onek run [flags] <task>`
  跑一次任务
- `onek chat [flags]`
  进入等待模式，可以连续提问；在交互终端里会默认进入全屏 TUI
- `onek models`
  查看模型列表
- `onek ping`
  测试接口和模型
- `onek version`
  查看版本

## 命令确认与 dangerously

- `confirm`
  默认模式。执行 shell 命令和写文件前都会确认。
- `dangerously`
  本次运行或本次会话里，跳过 shell 和写文件确认。

示例：

```bash
onek run --approval confirm "检查这个仓库"
onek run --dangerously "运行 go test ./..."
onek chat --dangerously
```

## 一句话运行

把下面命令里的接口地址和模型名换成你自己的即可。它们都不依赖 Node.js、Python 或其他额外运行时。

Linux x86_64:

```bash
curl -L https://github.com/axeprpr/onek-agent/releases/latest/download/onek-linux-amd64 -o ./onek && chmod +x ./onek && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='your-model' ./onek chat --auto-memory
```

Linux arm64:

```bash
curl -L https://github.com/axeprpr/onek-agent/releases/latest/download/onek-linux-arm64 -o ./onek && chmod +x ./onek && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='your-model' ./onek chat --auto-memory
```

macOS Intel:

```bash
curl -L https://github.com/axeprpr/onek-agent/releases/latest/download/onek-darwin-amd64 -o ./onek && chmod +x ./onek && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='your-model' ./onek chat --auto-memory
```

macOS Apple Silicon:

```bash
curl -L https://github.com/axeprpr/onek-agent/releases/latest/download/onek-darwin-arm64 -o ./onek && chmod +x ./onek && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='your-model' ./onek chat --auto-memory
```

Windows PowerShell x64:

```powershell
$env:MODEL_BASE_URL='http://127.0.0.1:11434/v1'; $env:MODEL_NAME='your-model'; Invoke-WebRequest https://github.com/axeprpr/onek-agent/releases/latest/download/onek-windows-amd64.exe -OutFile .\onek.exe; .\onek.exe chat --auto-memory
```

Windows PowerShell arm64:

```powershell
$env:MODEL_BASE_URL='http://127.0.0.1:11434/v1'; $env:MODEL_NAME='your-model'; Invoke-WebRequest https://github.com/axeprpr/onek-agent/releases/latest/download/onek-windows-arm64.exe -OutFile .\onek.exe; .\onek.exe chat --auto-memory
```

## 一句话安装

Linux 或 macOS：

```bash
curl -fsSL https://raw.githubusercontent.com/axeprpr/onek-agent/main/scripts/install.sh | bash
```

Windows PowerShell：

```powershell
iwr https://raw.githubusercontent.com/axeprpr/onek-agent/main/scripts/install.ps1 -UseBasicParsing | iex
```

国内网络可选加速：

如果你有自己可用的 `gh-proxy` 前缀，可以直接把它加在 GitHub 链接前面。`onek-agent` 不内置第三方代理地址，但命令格式就是这一种。

Linux 或 macOS：

```bash
GH_PROXY='https://your-gh-proxy/' && curl -fsSL "${GH_PROXY}https://raw.githubusercontent.com/axeprpr/onek-agent/main/scripts/install.sh" | bash
```

Windows PowerShell：

```powershell
$env:GH_PROXY='https://your-gh-proxy/'; iwr ($env:GH_PROXY + 'https://raw.githubusercontent.com/axeprpr/onek-agent/main/scripts/install.ps1') -UseBasicParsing | iex
```

安装后立即启动：

Linux 或 macOS：

```bash
curl -fsSL https://raw.githubusercontent.com/axeprpr/onek-agent/main/scripts/install.sh | bash && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='your-model' ~/.local/bin/onek chat --auto-memory
```

Windows PowerShell：

```powershell
iwr https://raw.githubusercontent.com/axeprpr/onek-agent/main/scripts/install.ps1 -UseBasicParsing | iex; $env:MODEL_BASE_URL='http://127.0.0.1:11434/v1'; $env:MODEL_NAME='your-model'; $HOME\.local\bin\onek.exe chat --auto-memory
```

如果你用 `gh-proxy` 加速 release 二进制，命令格式同样是：

Linux x86_64：

```bash
GH_PROXY='https://your-gh-proxy/' && curl -L "${GH_PROXY}https://github.com/axeprpr/onek-agent/releases/latest/download/onek-linux-amd64" -o ./onek && chmod +x ./onek && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='your-model' ./onek chat --auto-memory
```

可选安装变量：

- `ONEK_VERSION`
  安装指定版本，比如 `v0.1.2`
- `ONEK_INSTALL_DIR`
  安装到自定义目录

## 等待模式

`chat` 会保留上下文，所以它已经不只是“一次一问”的工具，而是一个轻量的持续会话 agent。

在交互终端里，`chat` 现在会直接打开全屏 TUI，包含：

- 顶部信息条，显示工作目录、shell、模型、审批模式
- 可滚动消息区
- 活动日志面板，可区分步骤、工具、错误、审批
- 窄终端下自动改为上下堆叠布局
- 独立的多行输入区
- 对回答里的 Markdown / 代码块做更好的终端渲染
- 底部状态栏
- 模型、审批模式、会话名、上下文剩余估算
- 结构化命令 / 文件写入审批弹窗
- `Tab` 可在消息区和活动面板之间切换焦点
- `f` 可切换活动日志过滤器

内置控制命令：

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

示例：

```text
onek> 看看这个项目是干什么的
onek> 接下来优先改什么
onek> /approval dangerously
onek> /remember 默认输出中文，简洁回答
onek> /remember-global 优先用简短中文回答
onek> /memorize
onek> /output terminal
onek> /reset
onek> 给我写个最小发布检查单
```

## 会话持久化

`chat` 默认会把会话保存在 `.onek-agent` 目录下。

- 会话状态：
  `.onek-agent/sessions/<session>.json`
- transcript 日志：
  `.onek-agent/transcripts/<session>.log`

常用参数：

- `--session <name>`
  使用指定会话名
- `--state-dir <path>`
  把状态和日志放到别的目录
- `--auto-memory`
  退出 chat 时自动把稳定上下文整理进记忆

## 持久化记忆

现在 `onek-agent` 已经支持一个轻量的长期记忆层。

它分成两个作用域：

- 全局记忆
  跨项目通用的偏好，比如语言、回答风格
- 项目记忆
  只绑定当前工作目录的规则和背景

你可以把稳定偏好、项目规则、长期背景信息记进去：

```text
onek> /remember-global 默认输出中文，简洁回答。
onek> /remember 这个项目优先支持 ARM64。
onek> /scope
onek> /memory
onek> /memorize
onek> /forget ARM64
```

记忆文件默认保存在：

- `.onek-agent/memory.json`

后续新的 chat 会话会把命中的记忆作为背景上下文注入到第一条 system prompt。

补充说明：

- 项目记忆按工作目录路径隔离
- `/memorize` 会把当前会话提炼成项目记忆
- `--auto-memory` 会在退出 chat 时自动执行这一步
- 如果模型侧的记忆总结超时或失败，`onek-agent` 会回退到本地提取明显的长期偏好和项目事实

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
