package request

// LLMProvider 是新增或完整修改大模型服务配置的请求。
type LLMProvider struct {
	Name        string   `json:"name"`
	BaseURL     string   `json:"base_url"`
	APIKey      string   `json:"api_key"`
	ClearAPIKey bool     `json:"clear_api_key"`
	Timeout     string   `json:"timeout"`
	Models      []string `json:"models"`
}

// LLMProviderEnabled 是单独切换启用状态的请求。
type LLMProviderEnabled struct {
	Enabled bool `json:"enabled"`
}
