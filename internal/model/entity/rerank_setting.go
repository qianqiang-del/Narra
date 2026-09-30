package entity

const (
	RerankTestStatusUntested = "untested"
	RerankTestStatusSuccess  = "success"
	RerankTestStatusFailed   = "failed"
)

// RerankSetting 是一个可保存的重排服务配置（每条配置对应一个重排模型）。
//
// 系统可保存多条，但同一时间只有一条 IsEnabled 为 true —— 由 IsEnabled 上的
// 部分唯一索引（WHERE is_enabled）在数据库层保证，检索精排只使用这条配置。
//
// 与向量模型的差异：切换重排模型不产生需要重建的存量数据（切片向量挂在向量模型下），
// 换模型只是换一个打分器，所以启停与切换是纯配置行为。
type RerankSetting struct {
	BaseModel

	Name            string  `gorm:"column:name;type:varchar(120);not null;unique;comment:配置名称，全局唯一" json:"name"`
	BaseURL         string  `gorm:"column:base_url;type:text;not null;comment:服务根地址（不含 /rerank 路径），如 https://api.siliconflow.cn/v1" json:"base_url"`
	Model           string  `gorm:"column:model;type:varchar(160);not null;comment:重排模型 ID，如 BAAI/bge-reranker-v2-m3" json:"model"`
	APIKeyEncrypted string  `gorm:"column:api_key_encrypted;type:text;not null;default:'';comment:加密后的 API Key；明文不落库，接口也不返回" json:"-"`
	TimeoutSeconds  int32   `gorm:"column:timeout_seconds;not null;default:2;check:rerank_settings_timeout_seconds_check,timeout_seconds BETWEEN 1 AND 600;comment:单次重排请求超时秒数，允许 1 ~ 600；超时即放弃本次精排" json:"timeout_seconds"`
	TestStatus      string  `gorm:"column:test_status;type:varchar(20);not null;default:untested;check:rerank_settings_test_status_check,test_status IN ('untested', 'success', 'failed');comment:连通性测试结果，取值 untested（没测过）/ success / failed；只有 success 才允许启用" json:"test_status"`
	LastTestError   *string `gorm:"column:last_test_error;type:text;comment:最近一次测试失败的报错原文；成功时为空" json:"last_test_error"`
	IsEnabled       bool    `gorm:"column:is_enabled;not null;default:false;uniqueIndex:rerank_settings_one_enabled_idx,where:is_enabled;comment:是否为当前生效的配置；全表最多一条为 true（部分唯一索引），检索精排只用这条" json:"is_enabled"`
}

func (RerankSetting) TableName() string { return "rerank_settings" }
