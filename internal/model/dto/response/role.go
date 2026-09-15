package response

type RoleItem struct {
	AgentKey  string `json:"agent_key"`  // 稳定标识，前端提交时用它
	Name      string `json:"name"`       // 展示名
	Role      string `json:"role"`       // 展示定位，如「主讲」
	RoleType  string `json:"role_type"`  // teacher | assistant | student
	Persona   string `json:"persona"`    // 信息卡正文
	Avatar    string `json:"avatar"`     // 头像路径
	Color     string `json:"color"`      // 主题色
	VoiceID   string `json:"voice_id"`   // 默认音色
	SortOrder int32  `json:"sort_order"` // 展示顺序，与数组顺序一致
}
