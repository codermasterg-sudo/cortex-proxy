package platform

import (
	"context"
	"sync"
	"time"
)

// OnConfigRefreshed 是配置刷新后的回调函数类型。
type OnConfigRefreshed func(cfg *ProxyConfig)

type ConfigManager struct {
	client    *Client
	interval  time.Duration
	onRefresh []OnConfigRefreshed

	mu      sync.RWMutex
	current *ProxyConfig
}

func NewConfigManager(client *Client, interval time.Duration) *ConfigManager {
	return &ConfigManager{client: client, interval: interval}
}

// OnRefresh 注册配置刷新回调，在每次成功拉取新配置后调用。
func (m *ConfigManager) OnRefresh(fn OnConfigRefreshed) {
	m.onRefresh = append(m.onRefresh, fn)
}

// SyncRefresh 同步执行一次配置拉取，用于启动时尽早获得平台配置。
func (m *ConfigManager) SyncRefresh(ctx context.Context) {
	m.refresh(ctx)
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

	for _, fn := range m.onRefresh {
		fn(cfg)
	}
}

func (m *ConfigManager) Get() *ProxyConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}
