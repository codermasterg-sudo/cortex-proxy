package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

type CompressResult struct {
	// json.RawMessage 保留平台返回的 messages 原始字节，避免 Go map 按字母序重新序列化打乱 key 顺序。
	Messages      json.RawMessage `json:"messages"`
	TokensBefore  int             `json:"tokens_before"`
	TokensAfter   int             `json:"tokens_after"`
	RecordID      string          `json:"record_id"`
	HasCCRMarkers bool            `json:"has_ccr_markers"`
}

type ReportingConfig struct {
	Fields          []string `json:"fields"`
	BatchSize       int      `json:"batch_size"`
	FlushIntervalMS int      `json:"flush_interval_ms"`
}

type CompressionConfig struct {
	Enabled   bool `json:"enabled"`
	TimeoutMS int  `json:"timeout_ms"`
}

type ProxyConfig struct {
	Compression CompressionConfig `json:"compression"`
	Reporting   ReportingConfig   `json:"reporting"`
}

type Client struct {
	baseURL string
	apiKey  string

	// httpClient 用于 config 拉取和 report 上报，固定超时。
	httpClient *http.Client
	// compressClient 用于压缩请求，不设 Timeout，完全由 context.WithTimeout 控制，
	// 避免 httpClient.Timeout 静默截断动态超时配置。
	compressClient *http.Client

	// compressTimeoutMS 存储压缩请求超时（毫秒），支持动态更新。
	compressTimeoutMS atomic.Int64
}

func NewClient(baseURL, apiKey string, defaultTimeoutMS int) *Client {
	c := &Client{
		baseURL:        baseURL,
		apiKey:         apiKey,
		httpClient:     &http.Client{Timeout: time.Duration(defaultTimeoutMS) * time.Millisecond},
		compressClient: &http.Client{}, // 无超时，由 context 控制
	}
	c.compressTimeoutMS.Store(int64(defaultTimeoutMS))
	return c
}

// UpdateCompressTimeout 动态更新压缩请求超时，由 ConfigManager 刷新后调用。
func (c *Client) UpdateCompressTimeout(timeoutMS int) {
	if timeoutMS > 0 {
		c.compressTimeoutMS.Store(int64(timeoutMS))
	}
}

// Compress 调用平台压缩 API。clientAgent 若非空则通过 X-Client-Agent 头传递给平台。
// instanceID 若非空则通过 X-Proxy-Instance-ID 头传递，平台记录到 compress_records。
// 压缩超时从 compressTimeoutMS 动态读取，可通过 UpdateCompressTimeout 实时调整。
func (c *Client) Compress(ctx context.Context, rawBody []byte, clientAgent string, instanceID string) (*CompressResult, error) {
	timeoutMS := c.compressTimeoutMS.Load()
	// Use context.Background() instead of the caller's ctx so that the compress
	// API call gets its own independent timeout. The caller's ctx (e.g., an
	// incoming HTTP request context) may already have a deadline that is too
	// short or already exceeded, which would cause the compress call to fail
	// immediately with "context deadline exceeded".
	compressCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(compressCtx, http.MethodPost, c.baseURL+"/v1/compress", bytes.NewReader(rawBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	if clientAgent != "" {
		req.Header.Set("X-Client-Agent", clientAgent)
	}
	if instanceID != "" {
		req.Header.Set("X-Proxy-Instance-ID", instanceID)
	}

	resp, err := c.compressClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("platform returned %d", resp.StatusCode)
	}

	var result CompressResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetConfig(ctx context.Context) (*ProxyConfig, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/proxy/config", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("platform returned %d", resp.StatusCode)
	}

	var cfg ProxyConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Client) Report(ctx context.Context, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/internal/report", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("platform returned %d", resp.StatusCode)
	}
	return nil
}
