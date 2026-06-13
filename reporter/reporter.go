package reporter

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

const flushHTTPTimeout = 1 * time.Second
const maxRetries = 3

type Reporter struct {
	platformURL string
	apiKey      string
	httpClient  *http.Client

	mu           sync.Mutex
	batchSize    int
	flushEvery   time.Duration
	buffer       []map[string]any
	flushing     bool // 防止并发 flush goroutine
	intervalCh   chan time.Duration
}

func New(platformURL, apiKey string, batchSize int, flushEvery time.Duration) *Reporter {
	return &Reporter{
		platformURL: platformURL,
		apiKey:      apiKey,
		httpClient:  &http.Client{Timeout: flushHTTPTimeout},
		batchSize:   batchSize,
		flushEvery:  flushEvery,
		intervalCh:  make(chan time.Duration, 1),
	}
}

// UpdateConfig 动态更新 batchSize 和 flushEvery（由 ConfigManager 刷新后调用）。
// flushEvery 变更会通过 intervalCh 通知 Start() 立即重建 ticker。
func (r *Reporter) UpdateConfig(batchSize int, flushEveryMS int) {
	r.mu.Lock()
	if batchSize > 0 {
		r.batchSize = batchSize
	}
	var newInterval time.Duration
	if flushEveryMS > 0 {
		newInterval = time.Duration(flushEveryMS) * time.Millisecond
		r.flushEvery = newInterval
	}
	r.mu.Unlock()

	if newInterval > 0 {
		select {
		case r.intervalCh <- newInterval:
		default: // 已有待处理的变更，丢弃旧值
		}
	}
}

func (r *Reporter) Enqueue(usage map[string]any) {
	r.mu.Lock()
	r.buffer = append(r.buffer, usage)
	shouldFlush := r.batchSize > 0 && len(r.buffer) >= r.batchSize
	r.mu.Unlock()
	if shouldFlush {
		go r.flush()
	}
}

func (r *Reporter) Start(ctx context.Context) {
	r.mu.Lock()
	interval := r.flushEvery
	r.mu.Unlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			go r.flush()
		case newInterval := <-r.intervalCh:
			ticker.Reset(newInterval)
		case <-ctx.Done():
			r.flush() // 关闭时同步 flush 确保数据不丢
			return
		}
	}
}

func (r *Reporter) flush() {
	r.mu.Lock()
	if r.flushing || len(r.buffer) == 0 {
		r.mu.Unlock()
		return
	}
	r.flushing = true
	batch := r.buffer
	r.buffer = nil
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		needsReflush := r.batchSize > 0 && len(r.buffer) >= r.batchSize
		r.flushing = false
		r.mu.Unlock()
		if needsReflush {
			go r.flush()
		}
	}()

	payload, err := json.Marshal(map[string]any{"llm_usages": batch})
	if err != nil {
		log.Printf("cortex-proxy: reporter marshal error: %v", err)
		return
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*200) * time.Millisecond)
		}
		req, err := http.NewRequest(http.MethodPost, r.platformURL+"/v1/internal/report", bytes.NewReader(payload))
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Authorization", "Bearer "+r.apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := r.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			lastErr = nil // 4xx 不重试（通常是认证问题，重试无意义）
			log.Printf("cortex-proxy: reporter flush got HTTP %d", resp.StatusCode)
		}
		return
	}
	if lastErr != nil {
		log.Printf("cortex-proxy: reporter flush failed after %d retries: %v", maxRetries, lastErr)
	}
}
