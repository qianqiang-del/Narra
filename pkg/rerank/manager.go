package rerank

import "sync"

// Manager 保存当前运行进程使用的一次精排连接（配置快照）。
//
// 与 embedding.Manager 同一种角色：设置页保存/启停后由服务层调用 Configure/Disable
// 热更新，检索侧每次请求向它要"当前生效的客户端"。nil 表示精排关闭 —— 检索链路
// 按原样进行，不加深候选、不改分数。
type Manager struct {
	mu     sync.RWMutex
	client *Client
}

// NewManager 创建一个空的管理器（精排关闭）。
func NewManager() *Manager { return &Manager{} }

// Configure 用配置构建客户端并替换当前生效能力。
// 配置非法时返回错误且保持原状：内存里那份要么是旧的完整配置、要么是新的完整配置，
// 不会半新半旧。
func (m *Manager) Configure(cfg Config) error {
	client, err := NewClient(cfg)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.client = client
	m.mu.Unlock()
	return nil
}

// Disable 关闭精排（当前没有启用配置）；重复调用是安全的。
func (m *Manager) Disable() {
	m.mu.Lock()
	m.client = nil
	m.mu.Unlock()
}

// Current 返回当前生效的客户端；nil 表示精排关闭。
func (m *Manager) Current() *Client {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.client
}
