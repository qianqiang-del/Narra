package service

// RoleItem 角色列表接口的响应项。
//
// 不复用 entity.PresetAgent：那个结构带着 id / enabled / created_at / updated_at，
// 前端一个都用不上（enabled 恒为 true，时间戳与展示无关），吐出去只是噪音。
//
// 也**不含系统提示词**——提示词不在 preset_agents 表里，本来就漏不出去。
//
// 不带 id：对外身份是 agent_key，不是数据库主键。带上 id 会让前端有机会拿主键当契约，
// 那样这个接口的主键暴露出去就再也换不掉了。
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
