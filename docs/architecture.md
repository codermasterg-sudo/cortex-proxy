# Cortex Proxy 架构文档

## 目录

- [项目概述](#项目概述)
- [整体架构](#整体架构)
- [模块结构](#模块结构)
- [核心流程](#核心流程)
  - [启动流程](#启动流程)
  - [请求拦截与压缩流程](#请求拦截与压缩流程)
  - [响应处理与使用量上报流程](#响应处理与使用量上报流程)
  - [CA 证书安装流程](#ca-证书安装流程)
  - [配置动态刷新流程](#配置动态刷新流程)
- [组件详解](#组件详解)
  - [cmd — 命令入口](#cmd--命令入口)
  - [cert — 证书管理](#cert--证书管理)
  - [platform — 平台客户端](#platform--平台客户端)
  - [proxy — 代理核心](#proxy--代理核心)
  - [reporter — 使用量上报](#reporter--使用量上报)
- [数据结构](#数据结构)
- [降级策略](#降级策略)
- [安全模型](#安全模型)
- [配置参考](#配置参考)
- [依赖关系](#依赖关系)

---

## 项目概述

`cortex-proxy` 是一个 HTTPS 中间人代理（MITM Proxy），运行在用户本地。它通过标准的 `HTTPS_PROXY` 环境变量拦截 AI Agent 发往 LLM 提供商的请求，将消息体发送至 Cortex 平台进行上下文压缩，再将压缩后的请求转发给真实 LLM。

**核心价值：零代码侵入**，用户无需修改 Agent 代码，只需设置环境变量即可接入压缩能力。

```
HTTPS_PROXY=http://localhost:7898
```

---

## 整体架构

```mermaid
graph TB
    subgraph 用户侧
        Agent["AI Agent / 用户代码"]
        Proxy["cortex-proxy\n(localhost:7898)"]
    end

    subgraph 外部服务
        Platform["Cortex 平台\napi.cortex.io"]
        LLM["LLM Provider\napi.anthropic.com\napi.openai.com"]
    end

    Agent -- "HTTPS 请求\n(via HTTPS_PROXY)" --> Proxy
    Proxy -- "POST /v1/compress\n(消息体)" --> Platform
    Platform -- "压缩后消息 + record_id" --> Proxy
    Proxy -- "压缩后请求\n(保留原始 Headers)" --> LLM
    LLM -- "原始响应" --> Proxy
    Proxy -- "透传响应" --> Agent
    Proxy -- "POST /v1/internal/report\n(usage 数据，异步批量)" --> Platform

    style Proxy fill:#4A90D9,color:#fff
    style Platform fill:#7B68EE,color:#fff
    style LLM fill:#52B788,color:#fff
```

---

## 模块结构

```mermaid
graph LR
    subgraph cortex-proxy
        main["main.go\n命令路由"]

        subgraph cmd["cmd/"]
            install["install.go\nCA 证书安装"]
            start["start.go\n代理启动"]
        end

        subgraph cert["cert/"]
            certgo["cert.go\nCA/叶证书生成"]
        end

        subgraph platform["platform/"]
            client["client.go\n平台 HTTP 客户端"]
            config["config.go\n配置动态管理"]
        end

        subgraph instance["instance/"]
            identity["identity.go\ninstance_id 持久化"]
        end

        subgraph proxy["proxy/"]
            proxygo["proxy.go\ngoproxy 包装 + MITM"]
            handler["handler.go\n请求拦截 + 压缩"]
            response["response.go\n响应 usage 提取"]
        end

        subgraph reporter["reporter/"]
            reportergo["reporter.go\n批量上报队列"]
        end
    end

    main --> cmd
    start --> instance
    start --> platform
    start --> proxy
    start --> reporter
    proxygo --> handler
    proxygo --> response
    handler --> client
    response --> reportergo
    config --> client
```

---

## 核心流程

### 启动流程

```mermaid
sequenceDiagram
    participant User as 用户
    participant Main as main.go
    participant Start as cmd/start.go
    participant CM as ConfigManager
    participant Rep as Reporter
    participant Proxy as ProxyServer

    User->>Main: cortex-proxy start --api-key=xxx
    Main->>Start: RunStart(args)
    Start->>Start: 解析 flags / 环境变量
    Start->>Start: instance.LoadOrCreate() ← 读取/生成持久化 instance_id
    Start->>CM: NewConfigManager(client, 5min)
    Start->>CM: SyncRefresh(ctx)  ← 同步拉取首次配置
    Note over CM: 失败时使用内置默认值
    Start->>Rep: New(platformURL, apiKey, batchSize, flushInterval)
    Start->>CM: OnRefresh(callback) ← 注册配置变更回调\n同时更新 Reporter 批参数 + Client 压缩超时
    Start->>CM: go Start(ctx)  ← 后台每5分钟刷新
    Start->>Rep: go Start(ctx)  ← 后台定时 flush
    Start->>Proxy: NewProxyServer(client, configMgr, rep, instanceID)
    Proxy->>Proxy: LoadCA() ← 从磁盘加载自签CA（补全 Leaf 字段）
    Start->>Start: http.ListenAndServe(:7898, proxy)
    Start-->>User: cortex-proxy listening on :7898 (instance=xxxxxxxx)
```

### 请求拦截与压缩流程

```mermaid
sequenceDiagram
    participant Agent as AI Agent
    participant GProxy as goproxy
    participant Handler as handler.go
    participant Platform as Cortex 平台
    participant LLM as LLM Provider

    Agent->>GProxy: CONNECT api.anthropic.com:443
    GProxy->>GProxy: MITM TLS 握手（用自签CA签发叶证书）
    Agent->>GProxy: POST /v1/messages (HTTPS)
    GProxy->>Handler: InterceptRequest(req)

    Handler->>Handler: 检查压缩配置是否启用
    alt 压缩未启用 或 非 JSON 请求
        Handler-->>GProxy: 返回原始 body
    else 压缩启用
        Handler->>Handler: io.ReadAll(req.Body)
        Handler->>Handler: mime.ParseMediaType → 确认 application/json
        Handler->>Platform: POST /v1/compress\n{messages, model}\nX-Client-Agent / X-Proxy-Instance-ID
        Note over Handler,Platform: 超时从 compressTimeoutMS 动态读取（atomic）\n每次请求创建独立 context.WithTimeout
        alt 平台可用
            Platform-->>Handler: {messages, record_id, tokens_before/after}
            Handler->>Handler: json.RawMessage 保留原始字段 + 替换 messages
            Handler-->>GProxy: 返回压缩后 body + record_id
        else 平台超时/错误
            Handler-->>GProxy: 返回原始 body（降级透传）
        end
    end

    GProxy->>GProxy: 存储 requestMeta{RecordID, StartTime} 至 ctx.UserData
    GProxy->>LLM: 转发请求（保留原始 Authorization 等 Headers）
```

### 响应处理与使用量上报流程

```mermaid
sequenceDiagram
    participant LLM as LLM Provider
    participant GProxy as goproxy
    participant Response as response.go
    participant Reporter as reporter.go
    participant Platform as Cortex 平台

    LLM-->>GProxy: HTTP 响应
    GProxy->>Response: ExtractAndEnqueueUsage(resp, recordID, fields, rep, ttfbMs, startTime)

    alt SSE 流式响应 (text/event-stream)
        Response->>Response: 替换 resp.Body 为 sseReadCloser
        Note over Response: 每次 Read 同步扫描完整行\n非阻塞发到 lineCh（chan string, 64）\nChannel 满时静默丢弃（不阻塞主流）
        Response->>Response: go goroutine 消费 lineCh
        Response-->>GProxy: 立即返回（resp.Body 已替换）
        GProxy-->>Agent: 开始透传流（sseReadCloser 透明转发）
        Note over Response: goroutine 聚合 fields\n流结束后上报（含 ttfb_ms/total_latency_ms）
        Response->>Reporter: Enqueue({record_id, ..., ttfb_ms, total_latency_ms})
    else 普通 JSON 响应
        Response->>Response: io.ReadAll(body)
        Response->>Response: json.Unmarshal
        Response->>Response: 按 fields 列表提取 token 字段
        Note over Response: 优先顶层字段\n再查 usage 嵌套对象（OpenAI格式）
        Response->>Reporter: Enqueue({record_id, input_tokens, ..., ttfb_ms, total_latency_ms})
        Response->>GProxy: 重写 resp.Body（原始内容还原）
    end

    GProxy-->>Agent: 透传响应

    Note over Reporter: 缓冲区满 batchSize 或超 flushInterval
    Reporter->>Reporter: flush()
    Reporter->>Platform: POST /v1/internal/report\n{llm_usages: [...]}
    alt 失败（最多重试3次，指数退避）
        Reporter->>Reporter: log 错误，丢弃批次
    end
```

### CA 证书安装流程

```mermaid
flowchart TD
    A[cortex-proxy install] --> B[cert.GenerateCA\nECDSA P-256]
    B --> C[写入 ~/.config/cortex-proxy/ca.crt\n权限 0644]
    C --> D[写入 ~/.config/cortex-proxy/ca.key\n权限 0600]
    D --> E{检测操作系统}
    E -->|macOS| F["security add-trusted-cert\n-k /Library/Keychains/System.keychain"]
    E -->|Linux| G["cp → /usr/local/share/ca-certificates/\nupdate-ca-certificates"]
    E -->|Windows| H["certutil -addstore Root ca.crt"]
    E -->|其他| I[提示手动信任]
    F --> J[✅ CA 已安装并信任]
    G --> J
    H --> J
    I --> K[⚠️ 需手动信任]

    style J fill:#52B788,color:#fff
    style K fill:#E76F51,color:#fff
```

### 配置动态刷新流程

```mermaid
sequenceDiagram
    participant Ticker as 5分钟定时器
    participant CM as ConfigManager
    participant Platform as Cortex 平台
    participant Rep as Reporter
    participant Start as Start() goroutine

    loop 每5分钟
        Ticker->>CM: 触发 refresh()
        CM->>Platform: GET /v1/proxy/config
        alt 成功
            Platform-->>CM: ProxyConfig{compression, reporting}
            CM->>CM: mu.Lock → 更新 m.current → mu.Unlock
            CM->>Rep: OnRefresh callback\nUpdateConfig(batchSize, flushIntervalMS)
            Rep->>Rep: 更新 batchSize/flushEvery
            Rep->>Start: intervalCh ← newInterval
            Start->>Start: ticker.Reset(newInterval) ← 立即生效
            CM->>CM: OnRefresh callback\nclient.UpdateCompressTimeout(timeoutMS)
            Note over CM: compressTimeoutMS atomic 更新\n下次压缩请求立即生效
        else 失败
            CM->>CM: 保留上次缓存，不触发回调
        end
    end
```

---

## 组件详解

### instance — 实例身份

`instance/identity.go` 提供 `LoadOrCreate()` 函数：

1. 从 `~/.config/cortex/instance-id`（权限 0600）读取已存储的 UUID
2. 若文件不存在或内容非法，生成新 UUID 并写入（目录不存在时自动创建，权限 0700）
3. 若配置目录不可写，回退为随机 UUID（仅当次运行有效）

`instance_id` 通过 `X-Proxy-Instance-ID` 请求头随每次压缩请求发往 Cortex 平台，平台可据此将来自同一机器的多次压缩记录关联在一起。

**存储路径：**
- Linux/macOS：`~/.config/cortex/instance-id`（`$XDG_CONFIG_HOME` 或 `$HOME/.config`）
- Windows：`%APPDATA%\cortex\instance-id`

---

### cmd — 命令入口

| 文件 | 职责 |
|------|------|
| `main.go` | 解析子命令（`install` / `start`），dispatch 到对应函数 |
| `cmd/install.go` | 生成并写入自签 CA 证书，调用 OS 信任命令 |
| `cmd/start.go` | 组装所有依赖（Client / ConfigManager / Reporter / ProxyServer），启动 HTTP 服务 |

**启动参数：**

| 参数 | 环境变量 | 默认值 | 说明 |
|------|---------|--------|------|
| `--api-key` | `CORTEX_API_KEY` | 必填 | Cortex 平台 API Key |
| `--platform` | `CORTEX_PLATFORM_URL` | `https://api.cortex.io` | 平台地址 |
| `--port` | — | `7898` | 代理监听端口 |

---

### cert — 证书管理

```mermaid
classDiagram
    class cert {
        +GenerateCA() *tls.Certificate
        +IssueForHost(ca, host) *tls.Certificate
        +ParseLeaf(der []byte) *x509.Certificate
    }
```

- **`GenerateCA()`**：生成 ECDSA P-256 自签 CA，有效期 10 年，`IsCA=true`
- **`IssueForHost()`**：由 CA 签发指定域名的叶证书，有效期 1 年，用于 MITM TLS
- **`ParseLeaf()`**：解析 DER 编码证书，用于补全 `tls.Certificate.Leaf` 字段，避免 goproxy 运行时重复解析

goproxy 在每次 CONNECT 隧道建立时调用 `IssueForHost` 动态签发证书，对每个目标域名都生成独立证书。`LoadCA` 加载磁盘证书后立即调用 `ParseLeaf` 补全 Leaf，确保 TLS 握手高效。

---

### platform — 平台客户端

```mermaid
classDiagram
    class Client {
        -baseURL string
        -apiKey string
        -httpClient *http.Client
        -compressClient *http.Client
        -compressTimeoutMS atomic.Int64
        +Compress(ctx, rawBody, clientAgent, instanceID) CompressResult
        +GetConfig(ctx) ProxyConfig
        +Report(ctx, payload) error
        +UpdateCompressTimeout(timeoutMS int)
    }

    class ConfigManager {
        -client *Client
        -interval time.Duration
        -current *ProxyConfig
        -onRefresh []OnConfigRefreshed
        -intervalCh chan Duration
        +SyncRefresh(ctx)
        +Start(ctx)
        +Get() *ProxyConfig
        +OnRefresh(fn)
    }

    class ProxyConfig {
        +Compression CompressionConfig
        +Reporting ReportingConfig
    }

    class CompressionConfig {
        +Enabled bool
        +TimeoutMS int
    }

    class ReportingConfig {
        +Fields []string
        +BatchSize int
        +FlushIntervalMS int
    }

    Client --> ProxyConfig : 返回
    ConfigManager --> Client : 调用 GetConfig
    ProxyConfig *-- CompressionConfig
    ProxyConfig *-- ReportingConfig
```

**超时说明：** `Client` 维护两套 HTTP 客户端：
- `httpClient`：固定超时（默认 3000ms），用于 config 拉取和 report 上报
- `compressClient`：无超时（由 `context.WithTimeout` 控制），压缩超时从 `compressTimeoutMS`（`atomic.Int64`）动态读取，可通过 `UpdateCompressTimeout` 实时调整，不需要重建客户端

---

### proxy — 代理核心

```mermaid
classDiagram
    class ProxyServer {
        +NewProxyServer(client, configMgr, rep, instanceID) *goproxy.ProxyHttpServer
        -LoadCA() *tls.Certificate
    }

    class requestMeta {
        +RecordID string
        +StartTime time.Time
    }

    class Handler {
        -client *platform.Client
        -configMgr *platform.ConfigManager
        -instanceID string
        +InterceptRequest(req) (newBody, recordID, err)
    }

    class ResponseModule {
        +ExtractAndEnqueueUsage(resp, recordID, fields, rep, ttfbMs, startTime)
        +GetReportingFields(cfg) []string
        +DefaultReportingFields []string
    }

    ProxyServer --> Handler : 请求拦截
    ProxyServer --> ResponseModule : 响应处理
    Handler --> platform.Client : 调用压缩
    ResponseModule --> reporter.Reporter : 入队 usage
```

**MITM 工作原理：**

1. 浏览器/Agent 发送 `CONNECT api.anthropic.com:443`
2. goproxy 截获，调用 `cert.IssueForHost("api.anthropic.com")` 签发假证书
3. 返回 `200 Connection Established`，与 Agent 完成 TLS 握手（用假证书）
4. 后续 HTTPS 请求明文可读，可修改 body

**注意：** MITM 配置为实例级（`mitmAction` 局部变量 + `HandleConnectFunc`），不覆盖 `goproxy` 全局变量，支持多实例并发运行。

---

### reporter — 使用量上报

```mermaid
stateDiagram-v2
    [*] --> Idle : New()

    Idle --> Buffering : Enqueue(usage)
    Buffering --> Buffering : Enqueue (buffer < batchSize)
    Buffering --> Flushing : buffer >= batchSize
    Idle --> Flushing : ticker.C 触发
    Buffering --> Flushing : ticker.C 触发

    Flushing --> Retrying : HTTP 失败
    Retrying --> Retrying : 重试 (最多3次，200ms*n 退避)
    Retrying --> Idle : 成功 or 达到最大重试
    Flushing --> Idle : 成功
    Flushing --> Flushing : flush 结束时 buffer >= batchSize\n(go re-flush)

    Idle --> Flushing : ctx.Done (同步flush)
    Flushing --> [*] : 关闭完成
```

**并发保护：** `flushing bool` 标志防止多个 goroutine 同时触发 flush（`Enqueue` 触发的 `go flush()` 与 ticker 触发的可能并发）。flush 结束后检查缓冲区，若仍达阈值则自动 `go flush()`（re-flush），确保高速写入场景下不积压。

**interval 动态更新：** `UpdateConfig` 通过带缓冲的 `intervalCh chan time.Duration` 通知 `Start()` goroutine 调用 `ticker.Reset()`，无需重启 goroutine。

---

## 数据结构

### CompressResult（来自平台）

```go
type CompressResult struct {
    Messages      []map[string]any // 压缩后的消息列表
    TokensBefore  int              // 压缩前 token 数
    TokensAfter   int              // 压缩后 token 数
    RecordID      string           // 平台记录 ID，用于关联 usage 上报
    HasCCRMarkers bool             // 是否插入了 CCR 召回标记
}
```

### Usage 上报格式

```json
{
  "llm_usages": [
    {
      "record_id": "uuid",
      "input_tokens": 300,
      "output_tokens": 100,
      "cache_read_tokens": 0,
      "cache_write_tokens": 0,
      "stop_reason": "end_turn",
      "ttfb_ms": 120,
      "total_latency_ms": 3500
    }
  ]
}
```

### ProxyConfig（来自平台）

```json
{
  "compression": {
    "enabled": true,
    "timeout_ms": 3000
  },
  "reporting": {
    "fields": ["input_tokens", "output_tokens", "stop_reason"],
    "batch_size": 10,
    "flush_interval_ms": 5000
  }
}
```

---

## 降级策略

```mermaid
flowchart LR
    A[请求到达] --> B{压缩配置\n已加载？}
    B -- 否（默认启用） --> C{调用平台\n压缩 API}
    B -- 是且禁用 --> Z[透传原始请求]
    C -- 成功 --> D[替换 body，转发]
    C -- 超时/网络错误 --> Z
    C -- 非200响应 --> Z
    C -- JSON解析失败 --> Z
    Z --> E[LLM 正常响应]
    D --> E
```

所有降级路径均返回原始 body，确保 Agent 工作流不受影响。

---

## 安全模型

```mermaid
graph TD
    subgraph 安全边界
        A["用户 LLM API Key\n(Authorization Header)"] -- "不转发给平台" --> B["cortex-proxy"]
        B -- "只转发 request body\n(messages, model)" --> C["Cortex 平台\n/v1/compress"]
        B -- "保留原始 Authorization" --> D["LLM Provider"]
    end

    E["自签 CA 证书\n(用户显式信任)"] --> B
    F["Cortex API Key\n(Bearer Token)"] --> C
```

**关键安全属性：**
- 用户的 LLM API Key 只在 proxy 内部中转，**不发送给 Cortex 平台**
- Cortex 平台只接收消息体（messages/model），不接收任何用户凭证
- MITM CA 需要用户主动运行 `cortex-proxy install` 并通过 OS 权限确认

---

## 配置参考

| 参数 | 来源 | 默认值 | 说明 |
|------|------|--------|------|
| `compression.enabled` | 平台配置 | `true` | 是否启用压缩 |
| `compression.timeout_ms` | 平台配置 | `3000` | 调平台压缩的超时（毫秒） |
| `reporting.batch_size` | 平台配置 | `10` | 批量上报条数阈值 |
| `reporting.flush_interval_ms` | 平台配置 | `5000` | 定时 flush 间隔（毫秒） |
| `reporting.fields` | 平台配置 | 见下 | 从响应中提取的字段列表 |

**默认上报字段：**
```
input_tokens, prompt_tokens,
output_tokens, completion_tokens,
cache_read_tokens, cache_write_tokens,
stop_reason
```

---

## 依赖关系

```mermaid
graph LR
    subgraph 直接依赖
        goproxy["github.com/elazarl/goproxy v1.7.0\nHTTPS MITM 代理框架"]
        uuid["github.com/google/uuid v1.6.0\nUUID 生成（instance_id）"]
        xnet["golang.org/x/net v0.34.0\nHTTP/2 支持"]
        xtext["golang.org/x/text v0.21.0\n字符编码（间接）"]
    end

    ProxyCore["proxy/ 包"] --> goproxy
    InstancePkg["instance/ 包"] --> uuid
    goproxy --> xnet
    xnet --> xtext
```

**运行时要求：**
- Go 1.22+
- 已运行 `cortex-proxy install`（CA 证书已安装）
- 有效的 Cortex API Key
- 网络可达 Cortex 平台（降级时不影响 LLM 调用）
