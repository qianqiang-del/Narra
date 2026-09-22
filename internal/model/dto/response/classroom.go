package response

import "time"

// ClassroomAgentBrief 是课堂里一个角色的最小信息：谁、用什么音色。
//
// 名称 / 头像 / 定位 / 人设不在这里，前端拿 AgentKey 去角色池取，一份数据一处来源；
// 只有 VoiceID 必须由后端给——角色池里那个是默认音色，本课程可能换过。
type ClassroomAgentBrief struct {
	AgentKey string `json:"agent_key"` // 角色标识，前端拿它去角色池取展示信息
	VoiceID  string `json:"voice_id"`  // 本课程为该角色选定的音色
}

// Classroom 是课堂的对外视图，受理返回与轮询查询共用。
type Classroom struct {
	ID              uint64                `json:"id"`
	FolderID        *uint64               `json:"folder_id"`
	Title           string                `json:"title"`
	Requirement     string                `json:"requirement"`
	Mode            string                `json:"mode"`
	Status          string                `json:"status"`
	GenerationError *string               `json:"generation_error"`
	Agents          []ClassroomAgentBrief `json:"agents"` // 本课程的角色，按角色池顺序；未指定时为空数组
	CreatedAt       time.Time             `json:"created_at"`
	UpdatedAt       time.Time             `json:"updated_at"`
}
