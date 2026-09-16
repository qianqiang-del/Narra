package embedding

import (
	"context"
	"sync"

	"narra/pkg/config"
)

// Manager 保存当前运行进程使用的 embedding 配置。
type Manager struct {
	mu  sync.RWMutex
	cfg config.EmbeddingConfig
}

// NewManager 使用给定的初始配置创建管理器。
func NewManager(cfg config.EmbeddingConfig) *Manager {
	return &Manager{cfg: cfg}
}

// Config 返回当前配置的副本。
func (m *Manager) Config() config.EmbeddingConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// Configure 校验并替换当前配置。
func (m *Manager) Configure(cfg config.EmbeddingConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg
	return nil
}

// Test 通过请求一个 embedding 校验配置。
func Test(ctx context.Context, cfg config.EmbeddingConfig) error {
	client, err := NewClient(cfg)
	if err != nil {
		return err
	}
	_, err = client.Embed(ctx, []string{"向量化服务连接测试"})
	return err
}
