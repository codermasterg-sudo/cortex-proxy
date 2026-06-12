package cmd

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/cortex-io/cortex-proxy/cert"
)

func RunInstall(args []string) {
	ca, err := cert.GenerateCA()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to generate CA: %v\n", err)
		os.Exit(1)
	}

	certDir, err := os.UserConfigDir()
	if err != nil {
		certDir = os.TempDir()
	}
	certDir = filepath.Join(certDir, "cortex-proxy")
	os.MkdirAll(certDir, 0700)

	certPath := filepath.Join(certDir, "ca.crt")
	keyPath := filepath.Join(certDir, "ca.key")

	// 写 CA cert
	caCert := ca.Leaf
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Raw})
	os.WriteFile(certPath, certPEM, 0644)

	// 写 CA key
	keyBytes, _ := x509.MarshalECPrivateKey(ca.PrivateKey.(*ecdsa.PrivateKey))
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	os.WriteFile(keyPath, keyPEM, 0600)

	// 写入系统信任链
	if err := trustCA(certPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not auto-trust CA: %v\n", err)
		fmt.Printf("Manually trust: %s\n", certPath)
	} else {
		fmt.Println("CA certificate installed and trusted.")
	}
	fmt.Printf("Cert: %s\nKey:  %s\n", certPath, keyPath)
}

func trustCA(certPath string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("security", "add-trusted-cert", "-d", "-r", "trustRoot",
			"-k", "/Library/Keychains/System.keychain", certPath).Run()
	case "linux":
		dest := "/usr/local/share/ca-certificates/cortex-proxy.crt"
		data, _ := os.ReadFile(certPath)
		os.WriteFile(dest, data, 0644)
		return exec.Command("update-ca-certificates").Run()
	case "windows":
		return exec.Command("certutil", "-addstore", "Root", certPath).Run()
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}
