package request

// RerankSetting 是新增或完整修改重排服务配置的请求。
//
// 一条配置对应一个重排模型；API Key 可选（自建服务常无鉴权），编辑时留空表示保持不变、
// clear_api_key 为真表示清除已保存的 Key。Timeout 是 Go 时长串（如 "2s"）。
type RerankSetting struct {
	Name        string `json:"name"`
	BaseURL     string `json:"base_url"`
	APIKey      string `json:"api_key"`
	ClearAPIKey bool   `json:"clear_api_key"`
	Timeout     string `json:"timeout"`
	Model       string `json:"model"`
}

// RerankSettingEnabled 是单独切换启用状态的请求。
type RerankSettingEnabled struct {
	Enabled bool `json:"enabled"`
}
