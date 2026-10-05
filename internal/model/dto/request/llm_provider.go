package request

import "time"

// LLMProvider 是新增或完整修改大模型服务配置的请求。
type LLMProvider struct {
	Name        string                  `json:"name"`
	BaseURL     string                  `json:"base_url"`
	APIKey      string                  `json:"api_key"`
	ClearAPIKey bool                    `json:"clear_api_key"`
	Timeout     string                  `json:"timeout"`
	Models      []string                `json:"models"`
	Pricing     map[string]ModelPricing `json:"pricing"`
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

type LLMPriceSuggestion struct {
	ModelID string `json:"model_id"`
}

// LLMProviderEnabled 是单独切换启用状态的请求。
type LLMProviderEnabled struct {
	Enabled bool `json:"enabled"`
}
