package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type CompressResult struct {
	Messages      []map[string]any `json:"messages"`
	TokensBefore  int              `json:"tokens_before"`
	TokensAfter   int              `json:"tokens_after"`
	RecordID      string           `json:"record_id"`
	HasCCRMarkers bool             `json:"has_ccr_markers"`
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
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewClient(baseURL, apiKey string, timeoutMS int) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: time.Duration(timeoutMS) * time.Millisecond,
		},
	}
}

func (c *Client) Compress(ctx context.Context, rawBody []byte) (*CompressResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/compress", bytes.NewReader(rawBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
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
	resp.Body.Close()
	return nil
}
