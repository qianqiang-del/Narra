package response

// DiscussionStart 是"讨论已受理"的应答。
//
// 只回两个 ID，不回讨论内容：讨论是**后台跑**的，这个请求返回时它才刚开始。
// 过程与结果都从事件流（GET /conversations/:id/events）里取，前端订阅它就能边跑边看。
//
// message_id 是用户那句话在 conversation_messages 里的行号，也是这次运行的
// 触发消息（orchestration_runs.trigger_message_id）—— 前端拿它可以就地把自己的
// 消息渲染出来，不必等事件流把它回传一遍。
type DiscussionStart struct {
	ConversationID uint64 `json:"conversation_id"` // 讨论发生在哪条对话里
	MessageID      uint64 `json:"message_id"`      // 刚写入的那条用户消息 ID
}
