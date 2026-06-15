# Cortex Proxy — CLAUDE.md

## 项目概述

`cortex-proxy` 是一个 Go 编写的 HTTP 反向代理，运行在用户本地。Agent 将 `OPENAI_BASE_URL`（或等效变量）指向代理，代理拦截请求、调用 Cortex 平台压缩消息，再将压缩后的请求转发给真实 LLM。

**无需证书**：Agent 通过 HTTP 与代理通信，代理通过 HTTPS 与上游 LLM 通信，整条链路无需安装任何 CA 证书。

**LLM key 和地址统一管理**：二者均在 `config.json` 中（或 CLI 参数），不依赖平台配置。

## 技术栈

- **语言：** Go 1.22
- **核心依赖：** `github.com/google/uuid v1.6.0`（实例 ID 生成），标准库 `net/http`
- **模块名：** `github.com/cortex-io/cortex-proxy`

## 目录结构

```
cortex-proxy/
├── main.go                  # 子命令路由（install / start）
├── Makefile
├── go.mod / go.sum
│
├── cmd/
│   ├── install.go           # 创建默认 config.json 模板
│   └── start.go             # 组装依赖，启动 HTTP 服务
│
├── config/
│   └── config.go            # Config 结构体 + DefaultPath() + Load()
│
├── instance/
│   └── identity.go          # instance_id 持久化（LoadOrCreate）
│
├── platform/
│   ├── client.go            # Compress / GetConfig / Report API 客户端
│   └── config.go            # 动态配置管理器（5 分钟轮询）
│
├── proxy/
│   ├── proxy.go             # HTTP 反向代理 Server（ServeHTTP）
│   ├── handler.go           # 请求拦截 + 调压缩 API
│   ├── response.go          # 响应 usage 提取（JSON + SSE 非阻塞旁路）
│   └── logger.go            # 结构化日志（logInfo/logWarn/logDebug）
│
├── reporter/
│   └── reporter.go          # 批量缓冲 + 定时 flush + 指数退避重试
│
└── dist/                    # 多平台编译输出
```

## 常用命令

```bash
# 构建本地二进制
make build

# 多平台构建（darwin-amd64/arm64, linux-amd64, windows-amd64）
make build-all

# 运行所有测试（含竞态检测）
make test

# 静态检查
make lint
```

## 使用方式

### 1. 初始化配置（首次使用）

```bash
cortex-proxy install
```

生成配置文件（路径因系统而异）：
- **macOS：** `~/Library/Application Support/cortex-proxy/config.json`
- **Linux：** `~/.config/cortex-proxy/config.json`（遵循 `$XDG_CONFIG_HOME`）
- **Windows：** `%AppData%\cortex-proxy\config.json`

### 2. 编辑配置

```json
{
  "upstream": {
    "base_url": "https://www.packyapi.com",
    "api_key": ""
  }
}
```

- `base_url`：LLM 提供商地址，代理将请求路径（`/v1/chat/completions` 等）拼接在其后
- `api_key`：**可选**。若设置，代理用此值替换 `Authorization` 头；若留空，原样透传 Agent 自身的 key

换 LLM 时：只改 `config.json`，key 和地址在同一个文件里。

### 3. 启动代理

```bash
# 从配置文件读取上游，必须提供 Cortex API key
cortex-proxy start --api-key=ctxp_sk_xxx

# 也可通过 CLI 参数覆盖配置文件
cortex-proxy start --api-key=ctxp_sk_xxx --upstream-url=https://api.openai.com

# 环境变量等效
CORTEX_API_KEY=ctxp_sk_xxx CORTEX_UPSTREAM_URL=https://api.openai.com cortex-proxy start
```

### 4. Agent 配置

```bash
# 永远指向 localhost:7898，无需因换 LLM 而修改
OPENAI_BASE_URL=http://localhost:7898
OPENAI_API_KEY=sk-your-llm-key        # 若 config.json 中未设置 api_key，此 key 会被透传
```

## 启动参数

| 参数 | 环境变量 | 默认值 | 说明 |
|------|---------|--------|------|
| `--api-key` | `CORTEX_API_KEY` | **必填** | Cortex 平台 API Key |
| `--platform` | `CORTEX_PLATFORM_URL` | `https://api.cortex.io` | 平台地址 |
| `--port` | — | `7898` | 监听端口 |
| `--upstream-url` | `CORTEX_UPSTREAM_URL` | config 文件 | LLM 上游 URL（优先级高于 config） |
| `--upstream-key` | `CORTEX_UPSTREAM_KEY` | config 文件 | LLM API Key（优先级高于 config） |
| `--config` | — | 系统默认路径 | 自定义 config.json 路径 |
| `--debug` | `CORTEX_DEBUG=1` | false | 开启 DEBUG 日志 |

## 核心数据流

```
AI Agent
  │ POST http://localhost:7898/v1/chat/completions
  │ Authorization: Bearer sk-user-key
  ▼
cortex-proxy (HTTP, 无需证书)
  │
  ├─→ POST /v1/compress → Cortex 平台（压缩 messages）
  │   ← 压缩后 messages + record_id
  │
  ├─→ 转发压缩后请求 → upstream LLM（HTTPS）
  │   Authorization: Bearer sk-user-key（或 config 中的 api_key）
  │   ← 原始响应（JSON 或 SSE）
  │
  └─→ 提取 usage → Reporter 缓冲 → POST /v1/internal/report（异步批量）
```

## 关键模块说明

### config/config.go — 配置加载

`DefaultPath()` 用 `os.UserConfigDir()` 获取跨平台配置目录，三大操作系统均适用。

`Load()` 若文件不存在返回空 Config（不报错），让 CLI 参数和环境变量覆盖生效。

### proxy/proxy.go — 反向代理

`Server.ServeHTTP` 处理每个请求：
1. 调 `handler.InterceptRequest` 压缩 body
2. 拼接目标 URL：`upstream.base_url` + 请求路径
3. 复制请求头（过滤 hop-by-hop），可选替换 `Authorization`
4. 发往上游，流式响应回 Agent（`flushWriter` 确保 SSE 逐块 flush）

### proxy/handler.go — 请求拦截

`InterceptRequest` 始终先读取 body（即使 compression disabled），保证调用方能拿到非 nil 的 body bytes：
1. 检查配置是否启用压缩（默认启用）
2. 校验 `Content-Type: application/json`
3. 调用 `platform.Client.Compress()`
4. 用 `json.RawMessage` 保留所有原始字段，只替换 `messages`，避免大整数精度丢失
5. 降级策略：平台超时/错误 → 透传原始 body，不影响 Agent 工作流

### proxy/response.go — 响应处理

区分两种响应：
- **普通 JSON：** 读取 body，解析 usage 字段，还原 body，附加延迟后上报
- **SSE 流式：** 替换为 `sseReadCloser`，旁路解析，独立 goroutine 消费，channel 满时静默丢弃，**不阻塞主流**

## 安全模型

- 用户的 **LLM API Key 不发送给 Cortex 平台**
- Cortex 平台只接收消息体（messages/model），不接收任何用户凭证
- `config.json` 权限 0600，仅当前用户可读

## 内置默认配置

| 配置项 | 默认值 |
|--------|--------|
| 压缩超时 | 3000ms |
| 批大小 | 10 |
| Flush 间隔 | 5000ms |
| 上报字段 | `input_tokens`, `prompt_tokens`, `output_tokens`, `completion_tokens`, `cache_read_tokens`, `cache_write_tokens`, `stop_reason` |

## 测试说明

```bash
go test -v -race ./...
```

各包均有独立测试：`instance/`、`platform/`、`proxy/`、`reporter/`。

## CI/CD

- **ci.yml：** 每个 push/PR 触发 `go test -race ./...` + `go vet ./...`
- **release.yml：** tag push 触发多平台构建 + GitHub Release 上传

## 注意事项

- 修改 `proxy/response.go` SSE 逻辑时，必须保证不阻塞原始响应流
- `platform.Client` 压缩超时通过 `atomic.Int64` 动态管理，由 context.WithTimeout 在每次请求时应用
- `reporter` 的 flush 并发依赖 `flushing` 标志；flush 结束后自动检查是否需要 re-flush
- 新增平台 API 调用时遵循降级模式：错误时透传，不向上 panic
