package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cortex-io/cortex-proxy/config"
	"github.com/cortex-io/cortex-proxy/platform"
	"github.com/cortex-io/cortex-proxy/reporter"
)

// Server is an HTTP reverse proxy that compresses LLM requests via the Cortex platform.
// Agents point OPENAI_BASE_URL (or equivalent) at this server's HTTP address.
// No TLS certificates required — the agent communicates with the proxy over plain HTTP,
// while the proxy connects to the upstream LLM over HTTPS.
type Server struct {
	handler   *Handler
	configMgr *platform.ConfigManager
	reporter  *reporter.Reporter
	upstream  config.UpstreamConfig
	client    *http.Client
}

func NewServer(
	platformClient *platform.Client,
	configMgr *platform.ConfigManager,
	rep *reporter.Reporter,
	instanceID string,
	upstream config.UpstreamConfig,
) *Server {
	return &Server{
		handler:   NewHandler(platformClient, configMgr, instanceID),
		configMgr: configMgr,
		reporter:  rep,
		upstream:  upstream,
		// No global timeout: individual requests use context deadlines set by the caller.
		client: &http.Client{Transport: &http.Transport{}},
	}
}

// hopByHop lists headers that must not be forwarded between proxy and upstream.
var hopByHop = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailers":            true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	logDebug("[REQ] %s %s (Content-Type: %s)", r.Method, r.URL.Path, r.Header.Get("Content-Type"))

	// Compress body via platform (reads + resets r.Body internally).
	newBody, recordID, _ := s.handler.InterceptRequest(r)

	// Build target URL: upstream base URL + incoming request path + query string.
	targetStr, err := buildTargetURL(s.upstream.BaseURL, r.URL)
	if err != nil {
		logWarn("[PROXY] invalid upstream URL: %v", err)
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	logDebug("[FORWARD] → %s", targetStr)

	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetStr, bytes.NewReader(newBody))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	upstreamReq.ContentLength = int64(len(newBody))

	// Forward all headers except hop-by-hop.
	for k, vv := range r.Header {
		if hopByHop[k] {
			continue
		}
		for _, v := range vv {
			upstreamReq.Header.Add(k, v)
		}
	}

	// Override Authorization header if upstream key is configured in config file.
	// Otherwise the agent's own Authorization header is forwarded unchanged.
	if s.upstream.APIKey != "" {
		upstreamReq.Header.Set("Authorization", "Bearer "+s.upstream.APIKey)
	}

	// Set Host to the upstream host, not localhost.
	parsed, _ := url.Parse(targetStr)
	upstreamReq.Host = parsed.Host

	resp, err := s.client.Do(upstreamReq)
	if err != nil {
		logWarn("[PROXY] upstream error: %v", err)
		http.Error(w, "bad gateway: "+err.Error(), http.StatusBadGateway)
		return
	}
	// 使用闭包而非 resp.Body.Close()，确保 defer 执行时读取 resp.Body 的当前值。
	// ExtractAndEnqueueUsage 在 SSE 场景下会将 resp.Body 替换为 sseReadCloser；
	// 若直接写 defer resp.Body.Close()，Go 会在 defer 注册时就求值方法接收者，
	// 捕获原始 body，导致 sseReadCloser.Close() 永远不被调用、lineCh 永远不关闭、
	// 消费 goroutine 永久泄漏，SSE usage 数据无法上报。
	defer func() { resp.Body.Close() }()

	ttfbMs := int(time.Since(start).Milliseconds())
	logDebug("[RESP] %d (record=%s, ttfb=%dms)", resp.StatusCode, recordID, ttfbMs)

	// Forward response headers.
	for k, vv := range resp.Header {
		if hopByHop[k] {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Side-channel usage extraction, then stream body to agent.
	// ExtractAndEnqueueUsage wraps resp.Body (SSE) or resets it (JSON) before the copy.
	fields := GetReportingFields(s.configMgr.Get())
	ExtractAndEnqueueUsage(resp, recordID, fields, s.reporter, ttfbMs, start)
	io.Copy(&flushWriter{w}, resp.Body)
}

// buildTargetURL builds the upstream request URL from the configured base and the incoming path.
//
// Overlap deduplication: if the upstream path is already a prefix of the request path,
// it is not doubled. This handles the common case where upstream.base_url includes "/v1"
// and the agent SDK also sends paths starting with "/v1".
//
//	upstream "https://www.packyapi.com/v1" + req "/v1/chat/completions"
//	  → "https://www.packyapi.com/v1/chat/completions"   (no double /v1)
//
//	upstream "https://api.openai.com" + req "/v1/chat/completions"
//	  → "https://api.openai.com/v1/chat/completions"
func buildTargetURL(upstream string, reqURL *url.URL) (string, error) {
	u, err := url.Parse(upstream)
	if err != nil {
		return "", fmt.Errorf("parse upstream %q: %w", upstream, err)
	}
	base := strings.TrimRight(u.Path, "/")
	req := reqURL.Path
	if base != "" && strings.HasPrefix(req, base+"/") {
		u.Path = req
	} else {
		u.Path = base + req
	}
	u.RawQuery = reqURL.RawQuery
	return u.String(), nil
}

// flushWriter wraps ResponseWriter and flushes after every write, which is required
// for SSE (server-sent events) streaming to reach the client incrementally.
type flushWriter struct{ http.ResponseWriter }

func (fw *flushWriter) Write(p []byte) (int, error) {
	n, err := fw.ResponseWriter.Write(p)
	if f, ok := fw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
	return n, err
}
