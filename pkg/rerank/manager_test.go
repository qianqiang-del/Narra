package rerank

import "testing"

// 配置合法：替换成功、当前客户端可用；关闭后回到 nil。
func TestManagerConfigureAndDisable(t *testing.T) {
	manager := NewManager()
	if manager.Current() != nil {
		t.Fatal("新建的管理器应当是关闭状态")
	}

	if err := manager.Configure(Config{BaseURL: "https://example.com/v1", Model: "m"}); err != nil {
		t.Fatalf("配置失败: %v", err)
	}
	client := manager.Current()
	if client == nil {
		t.Fatal("配置后应返回客户端")
	}
	if client.model != "m" || client.rerankURL != "https://example.com/v1/rerank" {
		t.Errorf("客户端字段不对: model=%q url=%q", client.model, client.rerankURL)
	}

	manager.Disable()
	if manager.Current() != nil {
		t.Error("关闭后当前客户端应为 nil")
	}
	manager.Disable() // 重复关闭不应 panic
}

// 非法配置不能把现有生效能力弄丢：返回错误、保持原状。
func TestManagerInvalidConfigKeepsCurrent(t *testing.T) {
	manager := NewManager()
	if err := manager.Configure(Config{BaseURL: "https://example.com/v1", Model: "m"}); err != nil {
		t.Fatalf("配置失败: %v", err)
	}
	before := manager.Current()

	if err := manager.Configure(Config{BaseURL: "", Model: "m"}); err == nil {
		t.Fatal("非法配置应当报错")
	}
	if manager.Current() != before {
		t.Error("非法配置不应替换掉现有客户端")
	}
}
