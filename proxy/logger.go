package proxy

import (
	"crypto/tls"
	"fmt"
	"log"
	"os"
	"sync/atomic"
)

var _debugEnabled atomic.Bool

// EnableDebug 开启 DEBUG 级日志，在 start 命令解析 --debug 标志后调用。
func EnableDebug() { _debugEnabled.Store(true) }

var _logger = log.New(os.Stderr, "", log.LstdFlags)

func logDebug(format string, args ...any) {
	if _debugEnabled.Load() {
		_logger.Printf("[DEBUG] "+format, args...)
	}
}

func logInfo(format string, args ...any) {
	_logger.Printf("[INFO]  "+format, args...)
}

func logWarn(format string, args ...any) {
	_logger.Printf("[WARN]  "+format, args...)
}

func tlsVersionName(ver uint16) string {
	switch ver {
	case tls.VersionTLS10:
		return "TLS1.0"
	case tls.VersionTLS11:
		return "TLS1.1"
	case tls.VersionTLS12:
		return "TLS1.2"
	case tls.VersionTLS13:
		return "TLS1.3"
	default:
		return fmt.Sprintf("TLS?(%#x)", ver)
	}
}
