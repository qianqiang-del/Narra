package entity

import (
	"encoding/json"
	"time"
)

const (
	LLMTestStatusUntested = "untested"
	LLMTestStatusSuccess  = "success"
	LLMTestStatusFailed   = "failed"
)

// LLMProvider 是一条 OpenAI 兼容大模型服务配置。
// V1 将模型 ID 列表直接保存在 Models JSON 中，按整条配置测试和启停。
//
// 四条 CHECK 由字段上的 check tag 声明，AutoMigrate 建；改取值时改这里即可。
type LLMProvider struct {
	BaseModel

	Name            string          `gorm:"column:name;type:varchar(120);not null;uniqueIndex" json:"name"`
	Protocol        string          `gorm:"column:protocol;type:varchar(32);not null;default:openai-compatible;check:llm_providers_protocol_check,protocol = 'openai-compatible'" json:"protocol"`
	BaseURL         string          `gorm:"column:base_url;type:text;not null" json:"base_url"`
	APIKeyEncrypted string          `gorm:"column:api_key_encrypted;type:text;not null;default:''" json:"-"`
	TimeoutSeconds  int32           `gorm:"column:timeout_seconds;not null;default:60;check:llm_providers_timeout_check,timeout_seconds BETWEEN 1 AND 600" json:"timeout_seconds"`
	Models          json.RawMessage `gorm:"column:models;type:jsonb;not null;default:'[]';check:llm_providers_models_check,jsonb_typeof(models) = 'array' AND jsonb_array_length(models) > 0" json:"models"`
	TestStatus      string          `gorm:"column:test_status;type:varchar(20);not null;default:untested;check:llm_providers_test_status_check,test_status IN ('untested', 'success', 'failed')" json:"test_status"`
	LastTestModel   *string         `gorm:"column:last_test_model;type:varchar(160)" json:"last_test_model"`
	LastTestError   *string         `gorm:"column:last_test_error;type:text" json:"last_test_error"`
	LastTestedAt    *time.Time      `gorm:"column:last_tested_at" json:"last_tested_at"`
	IsEnabled       bool            `gorm:"column:is_enabled;not null;default:false" json:"is_enabled"`
}

func (LLMProvider) TableName() string { return "llm_providers" }
