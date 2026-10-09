package request

// VLMSetting 是新增或完整修改视觉模型配置的请求。
//
// 一条配置对应一个视觉模型（VLM）；API Key 可选（自建服务常无鉴权），编辑时留空
// 表示保持不变、clear_api_key 为真表示清除已保存的 Key。Timeout 是 Go 时长串（如 "120s"）。
type VLMSetting struct {
	Name        string `json:"name"`
	BaseURL     string `json:"base_url"`
	APIKey      string `json:"api_key"`
	ClearAPIKey bool   `json:"clear_api_key"`
	Timeout     string `json:"timeout"`
	Model       string `json:"model"`
}

// VLMSettingProbe 是"测试连接"（不落库）的请求。
//
// 与保存不同：名称不参与校验（草稿允许还没起名）。编辑既有配置时带 ID ——
// API Key 留空表示沿用已保存的那把，免得"只是想测一下"还得重填密钥。
type VLMSettingProbe struct {
	ID          uint64 `json:"id"`
	BaseURL     string `json:"base_url"`
	APIKey      string `json:"api_key"`
	ClearAPIKey bool   `json:"clear_api_key"`
	Timeout     string `json:"timeout"`
	Model       string `json:"model"`
}

// VLMSettingEnabled 是单独切换启用状态的请求。
type VLMSettingEnabled struct {
	Enabled bool `json:"enabled"`
}
