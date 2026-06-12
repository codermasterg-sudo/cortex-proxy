package cert_test

import (
	"crypto/x509"
	"testing"

	"github.com/cortex-io/cortex-proxy/cert"
)

func TestGenerateCA(t *testing.T) {
	ca, err := cert.GenerateCA()
	if err != nil {
		t.Fatal(err)
	}
	if ca.Certificate == nil {
		t.Error("expected certificate")
	}
	if ca.PrivateKey == nil {
		t.Error("expected private key")
	}
}

func TestIssueCert(t *testing.T) {
	ca, err := cert.GenerateCA()
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := cert.IssueForHost(ca, "api.anthropic.com")
	if err != nil {
		t.Fatal(err)
	}
	// 验证 leaf cert 是由 CA 签发的
	pool := x509.NewCertPool()
	caCert, _ := x509.ParseCertificate(ca.Certificate[0])
	pool.AddCert(caCert)
	_, err = leaf.Leaf.Verify(x509.VerifyOptions{DNSName: "api.anthropic.com", Roots: pool})
	if err != nil {
		t.Errorf("cert verification failed: %v", err)
	}
}
