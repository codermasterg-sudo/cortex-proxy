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
	if err := os.MkdirAll(certDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create config dir %s: %v\n", certDir, err)
		os.Exit(1)
	}

	certPath := filepath.Join(certDir, "ca.crt")
	keyPath := filepath.Join(certDir, "ca.key")

	// 写 CA cert
	caCert := ca.Leaf
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Raw})
	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write CA cert to %s: %v\n", certPath, err)
		os.Exit(1)
	}

	// 写 CA key（类型断言加安全检查）
	ecKey, ok := ca.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		fmt.Fprintln(os.Stderr, "Unexpected CA private key type (expected ECDSA)")
		os.Exit(1)
	}
	keyBytes, err := x509.MarshalECPrivateKey(ecKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal CA key: %v\n", err)
		os.Exit(1)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write CA key to %s: %v\n", keyPath, err)
		os.Exit(1)
	}

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
		data, err := os.ReadFile(certPath)
		if err != nil {
			return fmt.Errorf("read cert: %w", err)
		}
		if err := os.WriteFile(dest, data, 0644); err != nil {
			return fmt.Errorf("write to system CA dir: %w", err)
		}
		return exec.Command("update-ca-certificates").Run()
	case "windows":
		return exec.Command("certutil", "-addstore", "Root", certPath).Run()
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}
