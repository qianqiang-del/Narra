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

	Name            string          `gorm:"column:name;type:varchar(120);not null;uniqueIndex;comment:配置名称，全局唯一" json:"name"`
	Protocol        string          `gorm:"column:protocol;type:varchar(32);not null;default:openai-compatible;check:llm_providers_protocol_check,protocol = 'openai-compatible';comment:服务方协议类型，目前只允许 openai-compatible" json:"protocol"`
	BaseURL         string          `gorm:"column:base_url;type:text;not null;comment:服务根地址，如 https://api.deepseek.com/v1" json:"base_url"`
	APIKeyEncrypted string          `gorm:"column:api_key_encrypted;type:text;not null;default:'';comment:加密后的 API Key；明文不落库，接口也不返回" json:"-"`
	TimeoutSeconds  int32           `gorm:"column:timeout_seconds;not null;default:60;check:llm_providers_timeout_check,timeout_seconds BETWEEN 1 AND 600;comment:单次调用超时秒数，允许 1 ~ 600" json:"timeout_seconds"`
	Models          json.RawMessage `gorm:"column:models;type:jsonb;not null;default:'[]';check:llm_providers_models_check,jsonb_typeof(models) = 'array' AND jsonb_array_length(models) > 0;comment:可用模型 ID 列表（JSON 数组，至少一个元素），按整条配置测试与启停" json:"models"`
	TestStatus      string          `gorm:"column:test_status;type:varchar(20);not null;default:untested;check:llm_providers_test_status_check,test_status IN ('untested', 'success', 'failed');comment:连通性测试结果，取值 untested（没测过）/ success / failed" json:"test_status"`
	LastTestModel   *string         `gorm:"column:last_test_model;type:varchar(160);comment:最近一次测试挑的模型 ID；没测过时为空" json:"last_test_model"`
	LastTestError   *string         `gorm:"column:last_test_error;type:text;comment:最近一次测试失败的报错原文；成功时为空" json:"last_test_error"`
	LastTestedAt    *time.Time      `gorm:"column:last_tested_at;comment:最近一次测试的时间，timestamptz 按 UTC 存" json:"last_tested_at"`
	IsEnabled       bool            `gorm:"column:is_enabled;not null;default:false;comment:是否启用该配置；只有启用的配置才会参与模型选择" json:"is_enabled"`
}

func (LLMProvider) TableName() string { return "llm_providers" }
