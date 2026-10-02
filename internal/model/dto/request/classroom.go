package request

// CreateClassroomMaterial 是本次生成附带的一份课程材料。
//
// 只带引用与展示快照：文件本体、解析产物、切片与向量都在知识库
// （knowledge_documents），这里只记 document_id，供生成时定向取用；
// name / size 是受理那一刻的快照，材料后来改名或删除也不影响回看。
type CreateClassroomMaterial struct {
	DocumentID uint64 `json:"document_id"` // 知识库文档 ID；必须是 ready 且启用的文档
	Name       string `json:"name"`        // 展示名；为空时服务端回落到文档标题
	Size       int64  `json:"size"`        // 文件字节数；前端快照，仅展示用
}

// CreateClassroom 是创建课堂（受理）的请求，字段与首页提交对齐。
type CreateClassroom struct {
	Requirement   string                    `json:"requirement"`     // 用户原始生成需求
	Mode          string                    `json:"mode"`            // vocational | interactive；空按 vocational
	LLMProviderID uint64                    `json:"llm_provider_id"` // 模型所属的大模型配置
	LLMModelID    string                    `json:"llm_model_id"`    // 模型 ID
	WebSearch     bool                      `json:"web_search"`      // 联网搜索开关
	Bio           string                    `json:"bio"`             // 用户简介，可空
	Materials     []CreateClassroomMaterial `json:"materials"`       // 上传的课程材料；空表示不带材料
	AgentMode     string                    `json:"agent_mode"`      // preset | auto；空按 preset
	RoleIDs       []string                  `json:"role_ids"`        // preset 模式下勾选的学员 agent_key
	RoleVoices    map[string]string         `json:"role_voices"`     // preset 模式下各角色的音色：agent_key → voice_id；缺省用角色默认音色
	TeacherVoice  string                    `json:"teacher_voice"`   // 教师音色 ID；缺省用教师默认音色
}
