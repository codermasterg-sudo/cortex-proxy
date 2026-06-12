package reporter_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cortex-io/cortex-proxy/reporter"
)

func TestReporterBatchesAndFlushes(t *testing.T) {
	received := make(chan []byte, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- body
		w.WriteHeader(202)
	}))
	defer srv.Close()

	r := reporter.New(srv.URL, "test-key", 2, 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	go r.Start(ctx)

	r.Enqueue(map[string]any{"record_id": "r1", "input_tokens": 100})
	r.Enqueue(map[string]any{"record_id": "r2", "input_tokens": 200})

	select {
	case body := <-received:
		var payload map[string]any
		json.Unmarshal(body, &payload)
		usages, ok := payload["llm_usages"].([]any)
		if !ok || len(usages) != 2 {
			t.Errorf("expected 2 usages, got %v", payload)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for report")
	}
	cancel()
}
