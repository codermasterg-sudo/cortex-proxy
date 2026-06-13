package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"

	"github.com/cortex-io/cortex-proxy/platform"
)

type Handler struct {
	client    *platform.Client
	configMgr *platform.ConfigManager
}

func NewHandler(client *platform.Client, configMgr *platform.ConfigManager) *Handler {
	return &Handler{client: client, configMgr: configMgr}
}

// InterceptRequest 拦截请求 body，调平台压缩，返回替换后的 body 和 record_id。
// 如果平台不可用或压缩禁用，返回原始 body。
// 安全保证：只把 request body（messages/model 等）发给 /v1/compress，不传原始 Authorization header。
func (h *Handler) InterceptRequest(req *http.Request) (newBody []byte, recordID string, err error) {
	cfg := h.configMgr.Get()
	if cfg != nil && !cfg.Compression.Enabled {
		return nil, "", nil
	}
	// cfg == nil 表示配置尚未加载，默认启用压缩（内置 default）

	rawBody, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, "", err
	}
	req.Body = io.NopCloser(bytes.NewReader(rawBody))

	// 只压缩 application/json（LLM API 请求），用 mime.ParseMediaType 剥离 charset 等参数
	mediaType, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return rawBody, "", nil
	}

	// 尽量获取 client agent：从 User-Agent 头提取，透传给平台
	clientAgent := req.Header.Get("User-Agent")

	result, err := h.client.Compress(req.Context(), rawBody, clientAgent)
	if err != nil {
		return rawBody, "", nil // 降级：透传原始 body
	}

	// 重组 body：用 json.RawMessage 保留所有原始字段的字节表示，只替换 messages。
	// 避免 map[string]any 的 float64 中转导致大整数精度丢失或格式变化。
	var original map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &original); err != nil {
		return rawBody, "", nil
	}
	messagesBytes, err := json.Marshal(result.Messages)
	if err != nil {
		return rawBody, "", nil
	}
	original["messages"] = json.RawMessage(messagesBytes)
	newBodyBytes, err := json.Marshal(original)
	if err != nil {
		return rawBody, "", nil
	}
	return newBodyBytes, result.RecordID, nil
}
