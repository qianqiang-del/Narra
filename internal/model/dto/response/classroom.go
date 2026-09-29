package response

import (
	"encoding/json"
	"time"
)

type ClassroomOutline struct {
	ClassroomID uint64         `json:"classroom_id"`
	Title       string         `json:"title"`
	Scenes      []OutlineScene `json:"scenes"`
}

type OutlineScene struct {
	ID        uint64 `json:"id"`
	SortOrder int32  `json:"sort_order"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Brief     string `json:"brief"`
	Status    string `json:"status"`
}

type ClassroomSceneSummary struct {
	ID        uint64 `json:"id"`
	SortOrder int32  `json:"sort_order"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	// Phase 是这一页生成到哪一步：status 说成不成，phase 说走到哪一步（取值见 entity.ScenePhase*）。
	// 生成进行页靠它把笼统的「正在生成」细分成检索资料/写内容/写讲稿/审核/合成语音。
	Phase        string  `json:"phase"`
	ErrorMessage *string `json:"error_message"`
}

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

// ClassroomCoverScene 是课堂列表卡片封面要画的首页内容，字段与场景详情同源、只少讲稿与审核结论。
//
// 列表封面要按主画布同款版式真实渲染，所以内容得整份带出来；前端按 Type 分发：
// slide / quiz 用 Content 里的块，interactive 用 InteractiveHTML 喂沙箱 iframe。
type ClassroomCoverScene struct {
	ID              uint64          `json:"id"`
	Type            string          `json:"type"`
	Title           string          `json:"title"`
	Status          string          `json:"status"`
	Content         json.RawMessage `json:"content"`
	InteractiveHTML string          `json:"interactive_html"`
}

// ClassroomListItem 是课堂列表的一项：课堂本身，加上卡片要用的页数与封面首页。
//
// 这两个字段只有列表端点填得出来——它们要按课程批量查场景，详情与受理返回不必为
// 一次卡片渲染多跑两条查询，所以留在列表项里，不并进 Classroom。
type ClassroomListItem struct {
	Classroom
	// Pages 是这门课的页数，不含代码在计划末尾追加的课程完成页。
	Pages int `json:"pages"`
	// ReadyPages 是其中已生成好的页数。大于 0 说明课堂进得去，卡片点进去直接进课堂，
	// 一页都没好则先去生成页等——判据只能是它，classrooms.status 的 playable 在页面就绪前就会置上。
	ReadyPages int `json:"ready_pages"`
	// Cover 是首个内容页的真实内容，供卡片渲染封面；大纲还没落库时为 nil。
	Cover *ClassroomCoverScene `json:"cover"`
}
