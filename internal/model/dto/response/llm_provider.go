package response

import "time"

type LLMProvider struct {
	ID               uint64                  `json:"id"`
	Name             string                  `json:"name"`
	Protocol         string                  `json:"protocol"`
	BaseURL          string                  `json:"base_url"`
	Timeout          string                  `json:"timeout"`
	Models           []string                `json:"models"`
	Pricing          map[string]ModelPricing `json:"pricing"`
	APIKeyConfigured bool                    `json:"api_key_configured"`
	TestStatus       string                  `json:"test_status"`
	LastTestModel    *string                 `json:"last_test_model"`
	LastTestError    *string                 `json:"last_test_error"`
	LastTestedAt     *time.Time              `json:"last_tested_at"`
	Enabled          bool                    `json:"enabled"`
	CreatedAt        time.Time               `json:"created_at"`
	UpdatedAt        time.Time               `json:"updated_at"`
}

type ModelPricing struct {
	InputPerMillion  *float64   `json:"input_per_million"`
	OutputPerMillion *float64   `json:"output_per_million"`
	Currency         string     `json:"currency"`
	Source           string     `json:"source"`
	PricingMode      string     `json:"pricing_mode,omitempty"`
	BillingNote      string     `json:"billing_note,omitempty"`
	ConfirmedAt      *time.Time `json:"confirmed_at,omitempty"`
	SourceURL        string     `json:"source_url,omitempty"`
	CheckedAt        *time.Time `json:"checked_at,omitempty"`
}

type LLMPriceCandidate struct {
	ModelID          string   `json:"model_id"`
	PricingMode      string   `json:"pricing_mode"`
	InputPerMillion  *float64 `json:"input_per_million,omitempty"`
	OutputPerMillion *float64 `json:"output_per_million,omitempty"`
	Currency         string   `json:"currency,omitempty"`
	Source           string   `json:"source,omitempty"`
	SourceURL        string   `json:"source_url,omitempty"`
	BillingNote      string   `json:"billing_note,omitempty"`
	Group            string   `json:"group,omitempty"`
	ModelRatio       *float64 `json:"model_ratio,omitempty"`
	CompletionRatio  *float64 `json:"completion_ratio,omitempty"`
	GroupRatio       *float64 `json:"group_ratio,omitempty"`
	Confidence       string   `json:"confidence,omitempty"`
}

type LLMPriceSuggestion struct {
	ModelID    string              `json:"model_id"`
	Found      bool                `json:"found"`
	Reason     string              `json:"reason,omitempty"`
	Candidates []LLMPriceCandidate `json:"candidates,omitempty"`
	// Pricing 保留给旧客户端；新客户端应使用 Candidates 并要求用户确认。
	Pricing *ModelPricing `json:"pricing,omitempty"`
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
