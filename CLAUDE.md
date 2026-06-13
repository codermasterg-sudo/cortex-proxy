# Cortex Proxy — CLAUDE.md

## 项目概述

`cortex-proxy` 是一个 Go 编写的 HTTPS 中间人代理（MITM Proxy），运行在用户本地。通过标准 `HTTPS_PROXY` 环境变量拦截 AI Agent 发往 LLM 提供商的请求，将消息体发送至 Cortex 平台进行上下文压缩，再将压缩后的请求转发给真实 LLM。

**核心价值：零代码侵入。** 用户无需修改 Agent 代码，只需设置环境变量即可接入压缩能力。

## 技术栈

- **语言：** Go 1.22
- **核心依赖：** `github.com/elazarl/goproxy v1.7.0`（HTTPS MITM 框架）、`github.com/google/uuid v1.6.0`（实例 ID 生成）
- **模块名：** `github.com/cortex-io/cortex-proxy`

## 目录结构

```
cortex-proxy/
├── main.go                  # 子命令路由（install / start）
├── Makefile                 # 构建脚本
├── go.mod / go.sum
│
├── cmd/
│   ├── install.go           # CA 证书生成 + 系统信任链安装
│   └── start.go             # 组装依赖，启动 HTTP 服务
│
├── cert/
│   └── cert.go              # GenerateCA / IssueForHost / ParseLeaf（ECDSA P-256）
│
├── instance/
│   └── identity.go          # instance_id 持久化（LoadOrCreate，存 ~/.config/cortex/instance-id）
│
├── platform/
│   ├── client.go            # Compress / GetConfig / Report API 客户端（压缩超时动态更新）
│   └── config.go            # 动态配置管理器（5 分钟轮询）
│
├── proxy/
│   ├── proxy.go             # goproxy 包装 + 实例级 MITM TLS 配置 + requestMeta 传递
│   ├── handler.go           # 请求拦截 + 调压缩 API（透传 instanceID）
│   └── response.go          # 响应 usage 提取（JSON + SSE 非阻塞旁路，含延迟字段）
│
├── reporter/
│   └── reporter.go          # 批量缓冲 + 定时 flush + 指数退避重试 + re-flush
│
├── docs/
│   └── architecture.md      # 详细架构文档（含 Mermaid 图）
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

# 一键格式化 + 检查
go fmt ./... && go vet ./...
```

## 启动参数

| 参数 | 环境变量 | 默认值 | 说明 |
|------|---------|--------|------|
| `--api-key` | `CORTEX_API_KEY` | **必填** | Cortex 平台 API Key |
| `--platform` | `CORTEX_PLATFORM_URL` | `https://api.cortex.io` | 平台地址 |
| `--port` | — | `7898` | 代理监听端口 |

```bash
# 安装 CA 证书（首次使用）
cortex-proxy install

# 启动代理
cortex-proxy start --api-key=xxx

# 客户端设置
export HTTPS_PROXY=http://localhost:7898
```

## 核心数据流

```
AI Agent
  │ CONNECT (via HTTPS_PROXY)
  ▼
cortex-proxy (localhost:7898)
  │ MITM TLS（自签 CA 签发叶证书）
  │
  ├─→ POST /v1/compress     → Cortex 平台
  │   ← 压缩后 messages + record_id
  │
  ├─→ 转发压缩后请求（保留原始 Authorization）→ LLM Provider
  │   ← 原始响应
  │
  └─→ 提取 usage → Reporter 缓冲 → POST /v1/internal/report（异步批量）
```

## 关键模块说明

### instance/identity.go — 实例身份

`LoadOrCreate` 读取或创建持久化的 `instance_id`（UUID v4），存储于 `~/.config/cortex/instance-id`（权限 0600）。启动时通过 `X-Proxy-Instance-ID` 请求头透传给 Cortex 平台压缩 API，平台可据此关联同一实例的多次压缩记录。若配置目录不可写，则回退为随机 UUID（当次运行有效，重启后变化）。

### proxy/handler.go — 请求拦截

`InterceptRequest` 是核心拦截逻辑：
1. 检查配置是否启用压缩（默认启用）
2. 校验 `Content-Type: application/json`
3. 调用 `platform.Client.Compress()`，透传 `instanceID`（→ `X-Proxy-Instance-ID`）和 `clientAgent`（→ `X-Client-Agent`）
4. 用 `json.RawMessage` 保留原始 JSON 字段字节表示，只替换 `messages`，避免 `float64` 中转导致大整数精度丢失
5. **降级策略：** 平台超时/错误/非 200/JSON 解析失败，均透传原始 body，不影响 Agent 工作流

### proxy/response.go — 响应处理

`ExtractAndEnqueueUsage` 区分两种响应类型：
- **普通 JSON：** `io.ReadAll` 后解析 usage 字段，还原 body，上报时附加 `ttfb_ms`/`total_latency_ms`
- **SSE 流式（`text/event-stream`）：** 替换为 `sseReadCloser`，每次 `Read` 时同步扫描完整行，非阻塞发到带缓冲 `chan string`（容量 64）；独立 goroutine 消费 channel、聚合字段后上报；channel 满时静默丢弃（usage 允许丢失），**绝不阻塞 Agent 接收主流**

字段提取优先级：顶层字段 > `usage` 嵌套对象（兼容 OpenAI/Anthropic 两种格式）

### reporter/reporter.go — 异步上报

- 缓冲区达到 `batchSize` 或超过 `flushInterval` 时触发 flush
- 失败重试：最多 3 次，指数退避（`200ms × attempt`）
- `flushing bool` 标志防止并发 flush；flush 完成后检查缓冲区，若仍达阈值则自动触发 re-flush（`go flush()`）
- 动态配置：通过带缓冲的 `intervalCh chan time.Duration` 通知主 goroutine 调用 `ticker.Reset()`，无需重启

### platform/config.go — 动态配置

- 每 5 分钟拉取一次 `/v1/proxy/config`
- 失败时保留上次缓存，不触发回调
- 支持 `SyncRefresh`：启动时同步拉取首次配置（失败使用内置默认值）
- 支持 `OnRefresh` 回调注册：配置变更时同时通知 Reporter 更新批参数、通知 `Client` 更新压缩超时（`UpdateCompressTimeout`）

## 安全模型

- 用户的 **LLM API Key 不发送给 Cortex 平台**，只在 proxy 内部中转
- Cortex 平台只接收消息体（messages/model），不接收任何用户凭证
- MITM CA 需要用户主动运行 `cortex-proxy install` 并通过 OS 权限确认

## 内置默认配置

| 配置项 | 默认值 |
|--------|--------|
| 压缩超时 | 3000ms |
| 批大小 | 10 |
| Flush 间隔 | 5000ms |
| 默认上报字段 | `input_tokens`, `prompt_tokens`, `output_tokens`, `completion_tokens`, `cache_read_tokens`, `cache_write_tokens`, `stop_reason` |

## 证书存储路径

- CA 证书：`~/.config/cortex-proxy/ca.crt`（权限 0644）
- CA 私钥：`~/.config/cortex-proxy/ca.key`（权限 0600）

## 测试说明

测试文件与被测文件同包，运行时启用竞态检测（`-race`）：

```bash
go test -v -race ./...
```

各包均有独立测试：`cert/cert_test.go`、`instance/identity_test.go`、`platform/client_test.go`、`platform/config_test.go`、`proxy/proxy_test.go`、`reporter/reporter_test.go`。

## CI/CD

- **ci.yml：** 每个 push/PR 到 main 触发 `go test -race ./...` + `go vet ./...`
- **release.yml：** tag push 触发多平台构建（`GOOS/GOARCH` 交叉编译）+ GitHub Release 上传

## 注意事项

- 修改 `proxy/response.go` 中 SSE 处理逻辑时，必须保证不阻塞原始响应流（Agent 应先于 usage 解析完成接收）
- `platform.Client` 的压缩超时（`compressTimeoutMS`）通过 `atomic.Int64` 独立管理，由 `context.WithTimeout` 在每次请求时应用；`httpClient` 固定超时仅用于 config 拉取和 report 上报，修改时注意两套超时的作用范围
- `reporter` 的 flush 并发保护依赖 `flushing` 标志，不要在 flush 逻辑外部读取缓冲区；flush 结束后会自动检查是否需要 re-flush
- 新增平台 API 调用时，遵循现有降级模式：错误时返回原始数据，记录日志，不向上传播 panic
- `proxy.go` 的 MITM 配置已改为实例级（`mitmAction` 局部变量 + `HandleConnectFunc`），不再覆盖 `goproxy.GoproxyCa` 等全局变量，多实例场景下互不干扰
