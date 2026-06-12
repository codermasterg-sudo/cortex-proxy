package platform

import (
	"context"
	"sync"
	"time"
)

type ConfigManager struct {
	client   *Client
	interval time.Duration
	mu       sync.RWMutex
	current  *ProxyConfig
}

func NewConfigManager(client *Client, interval time.Duration) *ConfigManager {
	return &ConfigManager{client: client, interval: interval}
}

func (m *ConfigManager) Start(ctx context.Context) {
	m.refresh(ctx)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.refresh(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (m *ConfigManager) refresh(ctx context.Context) {
	cfg, err := m.client.GetConfig(ctx)
	if err != nil {
		return // 保留上次缓存
	}
	m.mu.Lock()
	m.current = cfg
	m.mu.Unlock()
}

func (m *ConfigManager) Get() *ProxyConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}
