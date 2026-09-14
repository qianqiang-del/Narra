package entity

// ClassroomMemory 课程记忆，对应表 classroom_memories（设计文档 §4.8）。
//
// 本表只装课程记忆：用户偏好、课程设定这类跨会话还要用的信息，一条一句。它**不是**
// 对话摘要——摘要是会话级的单一属性，一个会话只有一份，存在 workbench_sessions.summary。
//
// 归属课程而不是会话：一门课下会有多条会话（§4.6），记忆要跨会话生效。挂会话的话，
// 用户在会话 A 里说的偏好，新开一个会话 B 就看不到了。能进本表的就代表「跨会话还要用」，
// 所以不需要再用一个字段标注记忆种类；会话级的东西不进来，它们由 workbench_messages
// （原话）和 workbench_sessions.summary（聊过的浓缩）承担。
//
// 写入由工作台 Agent 通过 remember 工具主动完成（判断标准与提示词要求见 §4.8）。
// 读取时按 classroom_id 取，ORDER BY id DESC LIMIT 50——表里不设上限也不删除，
// LIMIT 只是防止提示词失控后撑爆上下文的保险丝。
//
// 会话上下文以 PostgreSQL 为准，Redis 只缓存最近窗口：即使 Redis 被清空或过期，
// 也可以从 workbench_messages（原始消息）和 workbench_sessions.summary（滚动摘要）
// 恢复上下文（§1.1）。
type ClassroomMemory struct {
	BaseModel

	ClassroomID uint64 `gorm:"column:classroom_id;not null" json:"classroom_id"` // 所属课程；级联删除
	Content     string `gorm:"column:content;type:text;not null" json:"content"` // 记忆内容，自包含、不带指代的一整句话
}

// TableName 返回表名。
func (ClassroomMemory) TableName() string { return "classroom_memories" }
