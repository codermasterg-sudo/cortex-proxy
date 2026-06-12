package reporter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type Reporter struct {
	platformURL string
	apiKey      string
	batchSize   int
	flushEvery  time.Duration

	mu     sync.Mutex
	buffer []map[string]any
}

func New(platformURL, apiKey string, batchSize int, flushEvery time.Duration) *Reporter {
	return &Reporter{
		platformURL: platformURL,
		apiKey:      apiKey,
		batchSize:   batchSize,
		flushEvery:  flushEvery,
	}
}

func (r *Reporter) Enqueue(usage map[string]any) {
	r.mu.Lock()
	r.buffer = append(r.buffer, usage)
	shouldFlush := len(r.buffer) >= r.batchSize
	r.mu.Unlock()
	if shouldFlush {
		r.flush()
	}
}

func (r *Reporter) Start(ctx context.Context) {
	ticker := time.NewTicker(r.flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.flush()
		case <-ctx.Done():
			r.flush()
			return
		}
	}
}

func (r *Reporter) flush() {
	r.mu.Lock()
	if len(r.buffer) == 0 {
		r.mu.Unlock()
		return
	}
	batch := r.buffer
	r.buffer = nil
	r.mu.Unlock()

	payload, err := json.Marshal(map[string]any{"llm_usages": batch})
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, r.platformURL+"/v1/internal/report", bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}
