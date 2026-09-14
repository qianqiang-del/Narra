package entity

import "encoding/json"

// 课程模式 classrooms.mode（§4.2）。
const (
	ClassroomModeVocational  = "vocational"  // 职业技能
	ClassroomModeInteractive = "interactive" // 互动课堂
)

// 课程状态 classrooms.status（§5.1）。
//
// 只有四个值。曾经的 draft、outlining 已删除：课程建出来就开始生成，没有「等待用户确认大纲」
// 这一步；大纲和场景内容的生成在前端都是「正在生成」，用户看不出区别，后端自己知道走到哪一步
// 就够了，不需要往库里写。
const (
	ClassroomStatusGenerating = "generating" // 正在生成（大纲与场景内容算在一起），首页尚未就绪，进不去
	ClassroomStatusPlayable   = "playable"   // 首页已就绪，可以进入课堂；后续场景仍在生成，或流程已中断不再生成（看 GenerationError）
	ClassroomStatusReady      = "ready"      // 所有场景及其讲解均已生成，没有任何一页失败
	ClassroomStatusFailed     = "failed"     // 生成中断，且首页尚未就绪，课程不可进入
)

// Classroom 课程主记录，对应表 classrooms（设计文档 §4.2）。
//
// 课程是核心聚合根：场景、课程角色、讲解段落均从属于课程，删除课程时全部级联删除（§6）。
//
// 生成状态直接记在本表上：V1 一次生成只跑一趟，不并发、不重试、不取消、不支持断点续传，
// 所以没有独立的生成任务表——「这门课生成到哪了」就是 Status，「为什么没做完」就是
// GenerationError。将来若开放「对同一门课重新生成」，才会需要单独记录每一趟任务。
//
// 约束（§4.2）：CHECK (mode IN (...))、CHECK (status IN (...))，与 §5.1 一一对应；
// 增减状态值时必须同时改两处。写在 SQL migration 中。
type Classroom struct {
	BaseModel

	FolderID *uint64 `gorm:"column:folder_id" json:"folder_id"` // 所属文件夹，可空；删除文件夹时置 NULL

	Title       string `gorm:"column:title;type:varchar(200);not null" json:"title"`     // 课程名称
	Requirement string `gorm:"column:requirement;type:text;not null" json:"requirement"` // 用户原始生成需求
	Mode        string `gorm:"column:mode;type:varchar(32);not null" json:"mode"`        // vocational | interactive
	Status      string `gorm:"column:status;type:varchar(32);not null" json:"status"`    // 课程当前状态，见 §5.1

	// GenerationError 这次生成没能把课做完整的原因，给用户看的一句话；为空表示课程生成完整、
	// 不必再等。换句话说：这一列有值 ⇔ 这门课不会再生成了。
	//
	// 与 Status 是正交的两个问题：Status 只回答「能不能进课堂」，这一列只回答「还会不会继续
	// 生成」。所以它不只在 failed 上有值——首页已就绪（playable）时出问题，课程照样能进，
	// 只是剩下的场景不会再补上，这时 Status 留 playable、只写这一列。把这种课打成 failed 是错的，
	// 那等于把用户此前明明进得去的课给锁了。
	//
	// 两种写入时机都要照顾到，第二种尤其容易漏：
	//  1. 流程中断：进程挂了、崩溃了，剩下的场景永远不会再生成。
	//  2. 流程跑完了，但带着洞：某些页面生成失败被跳过（见 Scene.ErrorMessage），而 V1 没有重试，
	//     这些洞就永远留在那儿。流程自己跑到了终点，Status 停在 playable，没有任何东西会再来改它，
	//     不留记录的话前端会一直显示「正在生成」，等一个不会来的结果。
	// 相应地 Status 为 ready 的含义收紧为「全部完整生成，没有洞」。
	//
	// 与 scenes.error_message 分工不同：那边记的是「这一页为什么没生成出来」，记的时候整条流程
	// 还在往下跑；这里记的是「这门课为什么没做完」。大纲阶段挂掉时一条场景都还没建，scenes 表
	// 里空无一物，这一列是唯一的失败记录。
	// 同样只写能给人看的摘要，禁止写入原始 API 响应、堆栈和密钥，落库前按字符截断（建议 500）。
	// 用 text 而非 varchar：varchar 超长是报错，会把整个生成事务回滚掉。
	GenerationError *string `gorm:"column:generation_error;type:text" json:"generation_error"`

	// GenerationConfig 本次生成的模型、搜索、解析器等快照。
	GenerationConfig json.RawMessage `gorm:"column:generation_config;type:jsonb;not null;default:'{}'" json:"generation_config"`

	// AgentConfig 角色选择模式、自动生成策略与 TTS 配置。
	// 只保存本次采用的选择方式和生成策略；角色明细统一存 classroom_agents，不在此重复。
	AgentConfig json.RawMessage `gorm:"column:agent_config;type:jsonb;not null;default:'{}'" json:"agent_config"`
}

// TableName 返回表名。
func (Classroom) TableName() string { return "classrooms" }
