# onek-agent

`onek-agent` 是一个极简的单任务终端 agent。

它的目标很明确：

- 一次只跑一个任务
- 接 OpenAI 兼容 API
- 自带 shell、读写文件、grep、抓网页、简单 web search
- 不依赖 Node.js
- 直接发布单文件二进制

英文说明见 [README.md](/root/1k-install/README.md)。

## 功能

- `onek run [flags] <task>`
- `onek models`
- `onek ping`
- `onek version`

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

## 一句话运行

把下面命令里的接口地址和模型名换成你自己的即可。

Linux x86_64:

```bash
curl -L https://github.com/axeprpr/onek-agent/releases/latest/download/onek-linux-amd64 -o ./onek && chmod +x ./onek && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='qwen2.5-coder:7b' ./onek ping
```

Linux arm64:

```bash
curl -L https://github.com/axeprpr/onek-agent/releases/latest/download/onek-linux-arm64 -o ./onek && chmod +x ./onek && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='qwen2.5-coder:7b' ./onek ping
```

macOS Intel:

```bash
curl -L https://github.com/axeprpr/onek-agent/releases/latest/download/onek-darwin-amd64 -o ./onek && chmod +x ./onek && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='qwen2.5-coder:7b' ./onek ping
```

macOS Apple Silicon:

```bash
curl -L https://github.com/axeprpr/onek-agent/releases/latest/download/onek-darwin-arm64 -o ./onek && chmod +x ./onek && MODEL_BASE_URL='http://127.0.0.1:11434/v1' MODEL_NAME='qwen2.5-coder:7b' ./onek ping
```

Windows PowerShell x64:

```powershell
$env:MODEL_BASE_URL='http://127.0.0.1:11434/v1'; $env:MODEL_NAME='qwen2.5-coder:7b'; Invoke-WebRequest https://github.com/axeprpr/onek-agent/releases/latest/download/onek-windows-amd64.exe -OutFile .\onek.exe; .\onek.exe ping
```

Windows PowerShell arm64:

```powershell
$env:MODEL_BASE_URL='http://127.0.0.1:11434/v1'; $env:MODEL_NAME='qwen2.5-coder:7b'; Invoke-WebRequest https://github.com/axeprpr/onek-agent/releases/latest/download/onek-windows-arm64.exe -OutFile .\onek.exe; .\onek.exe ping
```

你的接口示例：

```bash
curl -L https://github.com/axeprpr/onek-agent/releases/latest/download/onek-linux-amd64 -o ./onek && chmod +x ./onek && MODEL_BASE_URL='https://llm.haohuapm.com:20020' MODEL_NAME='Qwen3.5-27B-FP8' ./onek ping
```

## 源码编译

```bash
go test ./...
go build ./...
go run ./cmd/onek version
```

## 发布

本地生成原始二进制：

```bash
./scripts/build-release.sh v0.1.1
```

GitHub 自动发版：

- 推一个 tag，比如 `v0.1.1`
- GitHub Actions 会自动构建 `linux`、`darwin`、`windows`
- 上传的资产是原始二进制，不再额外打 `zip` 或 `tar.gz`

## 状态

目前已经能用，但仍然是一个偏小的原型，还没有做完所有安全和交互细节。
