package request

// EmbeddingSetting 是保存或测试 Embedding 配置时所需的字段。
type EmbeddingSetting struct {
	BaseURL    string `json:"base_url"`
	Model      string `json:"model"`
	Timeout    string `json:"timeout"`
	Dimensions int    `json:"dimensions"`
	APIKey     string `json:"api_key"`
}
