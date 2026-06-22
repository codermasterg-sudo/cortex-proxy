package instance

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// LoadOrCreate 读取持久化的 instance_id。
// 若配置目录不可用或文件写入失败，则返回随机 UUID（当次运行有效，重启后会变）。
func LoadOrCreate() string {
	path, err := instanceIDPath()
	if err != nil {
		log.Printf("cortex-proxy: cannot determine config dir (%v), using random instance_id", err)
		return uuid.New().String()
	}

	if data, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(data))
		if _, err := uuid.Parse(id); err == nil {
			return id
		}
	}

	id := uuid.New().String()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		log.Printf("cortex-proxy: cannot create config dir (%v), using random instance_id", err)
		return id
	}
	if err := os.WriteFile(path, []byte(id), 0600); err != nil {
		log.Printf("cortex-proxy: cannot write instance_id (%v), using random instance_id", err)
	}
	return id
}

func instanceIDPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cortex-proxy", "instance-id"), nil
}
