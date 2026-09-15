package response

// EmbeddingSetting 是对外返回的配置；API Key 绝不返回明文。
type EmbeddingSetting struct {
	Name             string `json:"name"`
	BaseURL          string `json:"base_url"`
	Model            string `json:"model"`
	Timeout          string `json:"timeout"`
	Dimensions       int    `json:"dimensions"`
	APIKeyConfigured bool   `json:"api_key_configured"`
}
