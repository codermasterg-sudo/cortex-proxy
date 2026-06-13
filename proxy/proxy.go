package proxy

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cortex-io/cortex-proxy/cert"
	"github.com/cortex-io/cortex-proxy/platform"
	"github.com/cortex-io/cortex-proxy/reporter"
	"github.com/elazarl/goproxy"
)

// requestMeta 保存每个请求的上下文信息，通过 ctx.UserData 传递
type requestMeta struct {
	RecordID  string
	StartTime time.Time
}

func LoadCA() (*tls.Certificate, error) {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine user config dir: %w", err)
	}
	certPath := filepath.Join(cfgDir, "cortex-proxy", "ca.crt")
	keyPath := filepath.Join(cfgDir, "cortex-proxy", "ca.key")
	ca, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	ca.Leaf, _ = cert.ParseLeaf(ca.Certificate[0])
	return &ca, nil
}

func NewProxyServer(
	client *platform.Client,
	configMgr *platform.ConfigManager,
	rep *reporter.Reporter,
	instanceID string,
) (*goproxy.ProxyHttpServer, error) {
	ca, err := LoadCA()
	if err != nil {
		return nil, fmt.Errorf("CA not found — run `cortex-proxy install` first: %w", err)
	}

	handler := NewHandler(client, configMgr, instanceID)
	p := goproxy.NewProxyHttpServer()
	p.Verbose = false

	// 实例级 MITM 配置：通过 FuncHttpsHandler 避免覆写 goproxy 全局变量
	mitmAction := &goproxy.ConnectAction{
		Action:    goproxy.ConnectMitm,
		TLSConfig: goproxy.TLSConfigFromCA(ca),
	}
	p.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		return mitmAction, host
	})

	p.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		meta := &requestMeta{StartTime: time.Now()}
		newBody, recordID, _ := handler.InterceptRequest(req)
		if newBody != nil {
			req.Body = io.NopCloser(bytes.NewReader(newBody))
			req.ContentLength = int64(len(newBody))
		}
		meta.RecordID = recordID
		ctx.UserData = meta
		return req, nil
	})

	p.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		meta, _ := ctx.UserData.(*requestMeta)
		if meta == nil {
			return resp
		}
		fields := GetReportingFields(configMgr.Get())
		ttfbMs := int(time.Since(meta.StartTime).Milliseconds())
		ExtractAndEnqueueUsage(resp, meta.RecordID, fields, rep, ttfbMs, meta.StartTime)
		return resp
	})

	return p, nil
}
