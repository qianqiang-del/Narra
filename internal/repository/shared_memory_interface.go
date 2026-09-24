package repository

import (
	"context"

	"narra/internal/model/entity"
)

// SharedMemoryRepository 负责共享上下文记忆（shared_context_memories）的读写。
//
// 这张表存的是"这场讨论里已经确认过的事" —— 一小段结构化的话，不是聊天记录。
// 它和 conversation_messages 的区别在于：消息长了会被压缩、被截断，而记忆是**专门挑出来
// 单独保存的几条**，不参与压缩，所以每次发言都还带得上。
//
// 表上支持两种作用域（classroom / conversation）和三种状态（active / superseded / retracted），
// 但本接口只开两个方法 —— 当前版本只做"新增 + 读取生效的"：
//   - **没有 Update**：记忆一旦写下，就是那一刻的结论。事后改写它，会让"当时模型到底看过什么"
//     变得不可考。真要改，是"新增一条、把旧的标成 superseded"，那是将来做记忆取代时才补的方法。
//   - **没有 Delete**：撤销用状态表达（retracted）。删掉之后，连"它曾经存在过"都查不出来了。
type SharedMemoryRepository interface {
	// ListForContext 取这次发言可以带的记忆：**本条对话的对话级 + 同一门课的课堂级**，
	// 只算生效中（active）且未过期的，按重要度倒序。
	//
	// 两个 ID 都要传：记忆有两种作用域，只给 conversationID 查不出课堂级那一半。
	// limit 是"这次最多带几条"，**不是"库里只能存几条"** —— 记忆会和聊天记录一起进提示词，
	// 带太多就把正文的位置挤掉了。
	ListForContext(ctx context.Context, classroomID uint64, conversationID uint64, limit int) ([]entity.SharedContextMemory, error)

	// Create 写入一条新记忆，落库后回填 ID。
	//
	// 写入前调用方应先自行确认字段合法（类型在五个取值内、重要度 1~5、正文非空）：
	// 库上那几条 CHECK 会拦下不合法的数据，但它报出来的只是"某条约束被违反"，
	// 不如调用方自己判一下、把不合法的那条丢掉、再记一条日志来得清楚。
	Create(ctx context.Context, memory *entity.SharedContextMemory) error
}
