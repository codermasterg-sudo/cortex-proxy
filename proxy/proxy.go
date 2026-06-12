package proxy

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cortex-io/cortex-proxy/platform"
	"github.com/cortex-io/cortex-proxy/reporter"
	"github.com/elazarl/goproxy"
)

func LoadCA() (*tls.Certificate, error) {
	cfgDir, _ := os.UserConfigDir()
	certPath := filepath.Join(cfgDir, "cortex-proxy", "ca.crt")
	keyPath := filepath.Join(cfgDir, "cortex-proxy", "ca.key")
	ca, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	return &ca, nil
}

func NewProxyServer(
	client *platform.Client,
	configMgr *platform.ConfigManager,
	rep *reporter.Reporter,
) (*goproxy.ProxyHttpServer, error) {
	ca, err := LoadCA()
	if err != nil {
		return nil, fmt.Errorf("CA not found — run `cortex-proxy install` first: %w", err)
	}

	handler := NewHandler(client, configMgr)
	p := goproxy.NewProxyHttpServer()
	p.Verbose = false

	// 将自签 CA 注入 goproxy，用于 MITM TLS 签名
	goproxy.GoproxyCa = *ca
	// 重新初始化全局 MITM ConnectAction，使其使用我们的 CA
	goproxy.OkConnect = &goproxy.ConnectAction{
		Action:    goproxy.ConnectMitm,
		TLSConfig: goproxy.TLSConfigFromCA(ca),
	}
	goproxy.MitmConnect = &goproxy.ConnectAction{
		Action:    goproxy.ConnectMitm,
		TLSConfig: goproxy.TLSConfigFromCA(ca),
	}

	// 对所有 CONNECT 请求做 MITM
	p.OnRequest().HandleConnect(goproxy.AlwaysMitm)

	p.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		newBody, recordID, _ := handler.InterceptRequest(req)
		if newBody != nil {
			req.Body = io.NopCloser(bytes.NewReader(newBody))
			req.ContentLength = int64(len(newBody))
		}
		ctx.UserData = recordID
		return req, nil
	})

	p.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		recordID, _ := ctx.UserData.(string)
		fields := GetReportingFields(configMgr.Get())
		ExtractAndEnqueueUsage(resp, recordID, fields, rep)
		return resp
	})

	return p, nil
}
