package response

// EmbeddingSetting 是对外返回的配置；API Key 绝不返回明文。
type EmbeddingSetting struct {
	Name             string `json:"name"`
	BaseURL          string `json:"base_url"`
	Model            string `json:"model"`
	Timeout          string `json:"timeout"`
	Dimensions       int    `json:"dimensions"`
	APIKeyConfigured bool   `json:"api_key_configured"`

	// UsingPlatformDefault 表示该账号还没有自己的配置、当前生效的是平台默认
	// （config.yaml / 环境变量）。它是只读提示：平台默认不在设置页修改，
	// 填写并保存后即切换为该账号自己的配置。
	UsingPlatformDefault bool `json:"using_platform_default"`
}
