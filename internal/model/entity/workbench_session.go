package entity

// WorkbenchSession 工作台会话，对应表 workbench_sessions（设计文档 §4.6）。
//
// 工作台以课程为入口：会话必须归属一门课程，不存在任何无课程的会话；进入工作台首先
// 选择课程，选定后默认开一个新会话，历史会话按课程分组查看。
//
// ClassroomID 非空且为 CASCADE 而非 SET NULL——课程删除时会话一并删除，不保留孤儿会话（§6）。
// 会话在用户发出第一条消息时才落库；进入课程后未发言就离开不会产生空会话。
//
// 删除会话是硬删除，其下的 workbench_messages 随 CASCADE 一并删除（§6）。V1 不提供归档，
// 也不提供恢复——一个会话要么在，要么没了，所以本表既没有 status 也没有 deleted_at，
// 全库也一律不用软删除（§3）。
//
// Summary 与 SummaryUntilSequence 是本表的滚动摘要（§4.6）：它是会话级的单一属性，
// 一个会话只有一份，与 classroom_memories 里那些挂课程的长期记忆条目不是一回事。
//
// 工作台 Agent 可调用的工具是 web_search 与 doc_extract（web_search 与课堂 Agent 用的是
// 同一个），外加写入课程记忆的 remember（§4.8）。skill 是代码中的常量集合（如「精简讲解」
// 「补充练习」），不建表。
//
// 本表没有 agent_config：工作台 Agent 该有的配置项都不归会话管——模型由课程决定（同一门课
// 自始至终用一个模型，存在 classrooms.generation_config），而 skill 是「这一轮加载」的
// 消息级信息，会话级字段记不住它，何况前端目前根本没有加载入口。
//
// 注意：本表与 workbench_messages、classroom_memories 按 §10 第 12 项
// 本轮只设计、不立即建表，迁移脚本在后续迭代生成。
type WorkbenchSession struct {
	BaseModel

	ClassroomID uint64 `gorm:"column:classroom_id;not null" json:"classroom_id"`     // 所属课程；非空、级联删除
	Title       string `gorm:"column:title;type:varchar(200);not null" json:"title"` // 会话标题；写入前须按字符截断到 200 以内

	// Summary 滚动摘要：把已覆盖的历史消息压成的一段话，为 nil 表示还没有摘要。
	// 一个会话始终只有这一份——更新时把「已有摘要 + 这一批新消息」一起交给大模型重压，
	// 覆盖写回，不保留历史版本。摘要落库的理由不是怕 Redis 丢，而是重跑结果不稳定：
	// 大模型是概率的，同一批消息压两次得到的两段话不一样，上下文一变 Agent 的行为就漂移。
	Summary *string `gorm:"column:summary;type:text" json:"summary"`

	// SummaryUntilSequence 摘要已覆盖到的最后一条消息序号（workbench_messages.sequence）。
	// 与 Summary 同时为空或同时有值。拼上下文时只送 Sequence 大于它的原始消息，
	// 再加上这份摘要——它是「还有哪些消息没被覆盖」的判据；为空则全部消息照常发送。
	SummaryUntilSequence *int32 `gorm:"column:summary_until_sequence" json:"summary_until_sequence"`
}

// TableName 返回表名。
func (WorkbenchSession) TableName() string { return "workbench_sessions" }
