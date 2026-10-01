package request

// StartDiscussion 是在一条对话里发一句话、触发一趟多 Agent 讨论的请求。
//
// 正文之外只允许携带当前课件页 ID：用哪几个角色、用哪个大模型都不由前端决定 ——
// 角色取这堂课实际选定的那些，模型取这堂课生成时记下的快照，
// 两者都已经在库里了，再让前端传一遍只会多一个可能对不上的来源。
type StartDiscussion struct {
	Content string `json:"content"`  // 用户这句话的正文
	SceneID uint64 `json:"scene_id"` // 可选：发问时正在查看的课件页
}
