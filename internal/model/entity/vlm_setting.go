package entity

const (
	VLMTestStatusUntested = "untested"
	VLMTestStatusSuccess  = "success"
	VLMTestStatusFailed   = "failed"
)

// VLMSetting 是一条可保存的视觉模型（VLM）配置。
//
// 形态与 RerankSetting 一致：系统可保存多条，但同一时间只有一条 IsEnabled 为 true ——
// 由 IsEnabled 上的部分唯一索引（WHERE is_enabled）在数据库层保证。
//
// 用途不同：重排配置服务于检索精排；这条服务于**收录时的视觉理解** —— 文档解析时，
// 独立图片 / 扫描版 PDF 页 / docx 内嵌图会在本地 OCR 之外再调一次视觉模型生成描述。
// 停用或删除只是让解析退回纯 OCR，不产生需要重建的存量数据（描述是正文的一部分，
// 但换模型不需要重跑历史文档）。
type VLMSetting struct {
	BaseModel
	OwnerID uint64 `gorm:"column:owner_id;not null;index:idx_vlm_settings_owner_id;uniqueIndex:vlm_settings_owner_name_key,priority:1;uniqueIndex:vlm_settings_one_enabled_idx,priority:1,where:is_enabled" json:"-"`
	Owner   *User  `gorm:"foreignKey:OwnerID;constraint:vlm_settings_owner_id_fkey,OnDelete:RESTRICT" json:"-"`

	Name            string  `gorm:"column:name;type:varchar(120);not null;uniqueIndex:vlm_settings_owner_name_key,priority:2;comment:用户内唯一的配置名称" json:"name"`
	BaseURL         string  `gorm:"column:base_url;type:text;not null;comment:服务根地址（不含 /chat/completions 路径），如 https://api.siliconflow.cn/v1" json:"base_url"`
	Model           string  `gorm:"column:model;type:varchar(160);not null;comment:视觉模型 ID，如 Qwen/Qwen3.5-35B-A3B" json:"model"`
	APIKeyEncrypted string  `gorm:"column:api_key_encrypted;type:text;not null;default:'';comment:加密后的 API Key；明文不落库，接口也不返回" json:"-"`
	TimeoutSeconds  int32   `gorm:"column:timeout_seconds;not null;default:120;check:vlm_settings_timeout_seconds_check,timeout_seconds BETWEEN 1 AND 600;comment:单张图片的视觉请求超时秒数，允许 1 ~ 600；超时即降级为纯 OCR" json:"timeout_seconds"`
	TestStatus      string  `gorm:"column:test_status;type:varchar(20);not null;default:untested;check:vlm_settings_test_status_check,test_status IN ('untested', 'success', 'failed');comment:连通性测试结果，取值 untested（没测过）/ success / failed；只有 success 才允许启用" json:"test_status"`
	LastTestError   *string `gorm:"column:last_test_error;type:text;comment:最近一次测试失败的报错原文；成功时为空" json:"last_test_error"`
	IsEnabled       bool    `gorm:"column:is_enabled;not null;default:false;comment:是否为当前用户生效的配置" json:"is_enabled"`
}

func (VLMSetting) TableName() string { return "vlm_settings" }
