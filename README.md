# Cortex Proxy

**Cortex Proxy** 是一个运行在用户本地的 HTTP 反向代理。AI Agent 将请求地址指向 `http://localhost:7898`，代理自动拦截请求、调用 Cortex 平台对消息进行 token 压缩，再将压缩后的请求转发给真实 LLM。

压缩对 Agent 完全透明，不需要修改任何业务代码。

---

## 工作原理

```
AI Agent
  │  POST http://localhost:7898/v1/chat/completions
  ▼
cortex-proxy（本地，7898 端口）
  │
  ├─→ 调用 Cortex 平台 /v1/compress 压缩 messages
  │   ← 返回压缩后 messages + record_id
  │
  ├─→ 将压缩后的请求转发给真实 LLM（HTTPS）
  │   ← LLM 原始响应（JSON 或 SSE 流式）
  │
  └─→ 异步提取 usage 上报给 Cortex 平台（不阻塞响应）

AI Agent 收到 LLM 的原始响应，完全无感知压缩过程。
```

**安全说明：**
- Agent 的 LLM API Key 不发送给 Cortex 平台，Cortex 只接收消息体
- 配置文件权限为 0600，仅当前用户可读

---

## 安装

### 方式一：下载预编译二进制（推荐）

从 [GitHub Releases](https://github.com/cortex-io/cortex-proxy/releases/latest) 下载对应平台的二进制文件：

| 文件 | 系统 | 架构 |
|------|------|------|
| `cortex-proxy-darwin-amd64` | macOS | Intel |
| `cortex-proxy-darwin-arm64` | macOS | Apple Silicon (M1/M2/M3) |
| `cortex-proxy-linux-amd64` | Linux | x86_64 |
| `cortex-proxy-linux-arm64` | Linux | ARM64 / 树莓派 |
| `cortex-proxy-windows-amd64.exe` | Windows | x86_64 |

macOS / Linux 示例：

```bash
# 下载（以 macOS Apple Silicon 为例）
curl -L -o cortex-proxy https://github.com/cortex-io/cortex-proxy/releases/latest/download/cortex-proxy-darwin-arm64

# 赋予执行权限
chmod +x cortex-proxy

# 可选：移入 PATH
sudo mv cortex-proxy /usr/local/bin/
```

验证文件完整性（SHA-256，对照 `checksums.txt`）：

```bash
sha256sum cortex-proxy-darwin-arm64
```

### 方式二：从源码编译

需要 Go 1.22+：

```bash
git clone https://github.com/cortex-io/cortex-proxy.git
cd cortex-proxy
go build -o cortex-proxy .
```

---

## 快速开始

### 第一步：初始化配置

首次使用，运行 `install` 命令生成配置文件模板：

```bash
cortex-proxy install
```

配置文件路径：
- **macOS / Linux：** `~/.cortex-proxy/config.yaml`
- **Windows：** `C:\Users\<用户名>\.cortex-proxy\config.yaml`

### 第二步：编辑配置文件

```yaml
# ~/.cortex-proxy/config.yaml

listen:
  host: ""      # 监听地址，空表示 0.0.0.0（所有接口）
  port: 7898    # 监听端口

cortex:
  api_key: "ctxp_sk_xxx"              # Cortex 平台 API Key（必填）
  platform_url: ""                    # 留空使用默认 https://api.cortex.io

upstream:
  base_url: "https://api.openai.com"  # LLM 提供商地址（必填）
  api_key: ""                         # LLM API Key（可选，见说明）
```

> **upstream.api_key 说明：**
> - 填写时：代理用此 key 替换转发给 LLM 的 `Authorization` 头，可统一管理 LLM key
> - 留空时：Agent 自身的 `Authorization` 头原样透传给 LLM，由 Agent 自己持有 key

### 第三步：启动代理

```bash
cortex-proxy start
```

也可以不使用配置文件，完全通过 CLI 参数或环境变量启动：

```bash
# CLI 参数
cortex-proxy start \
  --api-key=ctxp_sk_xxx \
  --upstream-url=https://api.openai.com

# 环境变量
export CORTEX_API_KEY=ctxp_sk_xxx
export CORTEX_UPSTREAM_URL=https://api.openai.com
cortex-proxy start
```

### 第四步：配置 Agent

将 Agent 的 LLM 地址指向代理：

```bash
export OPENAI_BASE_URL=http://localhost:7898
export OPENAI_API_KEY=sk-your-llm-key   # 若 upstream.api_key 未设置则需要带上
```

以 Claude Code 为例：

```bash
OPENAI_BASE_URL=http://localhost:7898 claude
```

以 Python SDK 为例：

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:7898/v1",
    api_key="sk-your-llm-key",
)
```

---

## 配置项参考

### 配置文件 (`~/.cortex-proxy/config.yaml`)

| 配置项 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `listen.host` | string | `""` (0.0.0.0) | 监听地址，空表示所有网络接口 |
| `listen.port` | int | `7898` | 监听端口 |
| `cortex.api_key` | string | — | Cortex 平台 API Key，以 `ctxp_sk_` 开头 |
| `cortex.platform_url` | string | `https://api.cortex.io` | Cortex 平台地址，本地开发用 `http://localhost:8000` |
| `upstream.base_url` | string | — | LLM 提供商 base URL，**必填** |
| `upstream.api_key` | string | `""` | LLM API Key，留空则透传 Agent 的 Authorization 头 |

### `cortex-proxy start` CLI 参数

所有参数都是可选的；优先级：CLI 参数 > 环境变量 > 配置文件 > 内置默认值。

| 参数 | 环境变量 | 默认值 | 说明 |
|------|---------|--------|------|
| `--api-key=<key>` | `CORTEX_API_KEY` | 配置文件 | Cortex 平台 API Key（必须通过三者之一提供）|
| `--platform=<url>` | `CORTEX_PLATFORM_URL` | `https://api.cortex.io` | Cortex 平台地址 |
| `--upstream-url=<url>` | `CORTEX_UPSTREAM_URL` | 配置文件 | LLM 上游 base URL（必须通过三者之一提供）|
| `--upstream-key=<key>` | `CORTEX_UPSTREAM_KEY` | 配置文件 | LLM API Key（可选）|
| `--host=<host>` | — | `""` (0.0.0.0) | 监听地址 |
| `--port=<port>` | — | `7898` | 监听端口 |
| `--config=<path>` | — | `~/.cortex-proxy/config.yaml` | 自定义配置文件路径 |
| `--debug` | `CORTEX_DEBUG=1` | `false` | 开启 DEBUG 级别日志 |

### 内置默认值（由 Cortex 平台动态下发，可远程调整）

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| 压缩超时 | 3000ms | 调用 Cortex 平台压缩 API 的超时时间 |
| 上报批大小 | 10 条 | usage 批量上报时每批的最大条数 |
| Flush 间隔 | 5000ms | usage 强制 flush 的时间间隔 |

---

## 支持的 LLM 提供商

代理在 HTTP 层工作，兼容所有使用 OpenAI 兼容格式（`/v1/chat/completions`）的 LLM 服务：

| 提供商 | `upstream.base_url` |
|--------|---------------------|
| OpenAI | `https://api.openai.com` |
| Anthropic（兼容端点） | `https://api.anthropic.com` |
| DeepSeek | `https://api.deepseek.com` |
| 阿里云百炼 | `https://dashscope.aliyuncs.com/compatible-mode` |
| Azure OpenAI | `https://<resource>.openai.azure.com` |
| 自托管 / 第三方转发 | `https://your-llm-endpoint.com` |

---

## 日志

代理启动时自动在二进制所在目录下创建 `logs/` 文件夹，日志按天滚动：

```
<cortex-proxy 所在目录>/
└── logs/
    ├── cortex-proxy-2025-06-20.log
    └── cortex-proxy-2025-06-21.log
```

同时输出到 stderr（可在终端直接查看）。开启 `--debug` 后会额外输出每次请求的压缩详情。

---

## 常见问题

### Q: 启动报错 "upstream URL is required"

必须通过以下方式之一提供 LLM 上游地址：

```bash
# 方式 1：CLI 参数
cortex-proxy start --upstream-url=https://api.openai.com

# 方式 2：环境变量
export CORTEX_UPSTREAM_URL=https://api.openai.com

# 方式 3：配置文件 upstream.base_url 字段
cortex-proxy install  # 生成配置文件，然后编辑
```

### Q: 启动报错 "Cortex API key is required"

同上，通过 `--api-key`、`CORTEX_API_KEY` 或配置文件 `cortex.api_key` 提供。

### Q: Agent 请求没有被压缩

查看代理日志（`logs/cortex-proxy-<日期>.log`），搜索 `compress` 关键字：
- 若看到 `compression disabled by platform config`：平台配置了不压缩
- 若看到 `compress failed`：检查 Cortex API Key 是否有效，平台是否可达
- 若看到 `Content-Type not application/json`：Agent 发送的请求格式不支持

### Q: 代理和 Agent 之间是否需要 HTTPS？

不需要。Agent 到代理走 HTTP（`http://localhost:7898`），代理到 LLM 走 HTTPS。无需安装任何证书。

### Q: 换一个 LLM 提供商怎么操作？

只需修改配置文件中的 `upstream.base_url`（和 `api_key`），重启代理。Agent 侧的 `OPENAI_BASE_URL` 保持 `http://localhost:7898` 不变。

### Q: 支持流式响应（SSE）吗？

完全支持。代理对 SSE 流式响应采用旁路解析，不阻塞原始流，Agent 感受不到任何额外延迟。

---

## 本地开发 / 从源码构建

```bash
# 运行测试（含竞态检测）
go test -v -race ./...

# 本地构建
go build -o cortex-proxy .

# 多平台构建
make build-all

# 连接本地 Cortex 平台（docker-compose up）
cortex-proxy start \
  --platform=http://localhost:8000 \
  --api-key=test-key \
  --upstream-url=https://api.openai.com
```

---

## 相关链接

- [Cortex Platform](../cortex-platform/) — 压缩服务端 + 管理控制台
- [完整架构文档](docs/architecture.md)
