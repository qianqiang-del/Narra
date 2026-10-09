package response

import "time"

// VLMSetting 是视觉模型配置的响应结构；API Key 只透出「是否已配置」这个布尔值，
// 密文与明文都不出服务层。
type VLMSetting struct {
	ID               uint64    `json:"id"`
	Name             string    `json:"name"`
	BaseURL          string    `json:"base_url"`
	Timeout          string    `json:"timeout"`
	Model            string    `json:"model"`
	APIKeyConfigured bool      `json:"api_key_configured"`
	TestStatus       string    `json:"test_status"`
	LastTestError    *string   `json:"last_test_error"`
	Enabled          bool      `json:"enabled"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// VLMSettingTestResult 是测试连接的结果。失败走统一错误响应（400），
// 这个结构只在成功时返回；Message 带上模型的实际回复片段，便于用户确认端点真的在响应。
type VLMSettingTestResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}
