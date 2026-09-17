package response

import "time"

type LLMProvider struct {
	ID               uint64     `json:"id"`
	Name             string     `json:"name"`
	Protocol         string     `json:"protocol"`
	BaseURL          string     `json:"base_url"`
	Timeout          string     `json:"timeout"`
	Models           []string   `json:"models"`
	APIKeyConfigured bool       `json:"api_key_configured"`
	TestStatus       string     `json:"test_status"`
	LastTestModel    *string    `json:"last_test_model"`
	LastTestError    *string    `json:"last_test_error"`
	LastTestedAt     *time.Time `json:"last_tested_at"`
	Enabled          bool       `json:"enabled"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type AvailableLLMModel struct {
	ProviderID   uint64 `json:"provider_id"`
	ProviderName string `json:"provider_name"`
	ModelID      string `json:"model_id"`
}

type LLMProviderTestResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}
