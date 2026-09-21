package request

import "encoding/json"

// CreateClassroom 是创建课堂（受理）的请求，字段与首页提交对齐。
type CreateClassroom struct {
	Requirement   string          `json:"requirement"`     // 用户原始生成需求
	Mode          string          `json:"mode"`            // vocational | interactive；空按 vocational
	LLMProviderID uint64          `json:"llm_provider_id"` // 模型所属的大模型配置
	LLMModelID    string          `json:"llm_model_id"`    // 模型 ID
	WebSearch     bool            `json:"web_search"`      // 联网搜索开关
	Bio           string          `json:"bio"`             // 用户简介，可空
	Materials     json.RawMessage `json:"materials"`       // 上传的课程材料，V1 先存不消费
	AgentMode     string          `json:"agent_mode"`      // preset | auto；空按 preset
	RoleIDs       []string        `json:"role_ids"`        // preset 模式下勾选的学员 agent_key
	TeacherVoice  string          `json:"teacher_voice"`   // 教师音色 ID
}
