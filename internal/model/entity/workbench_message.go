package entity

import "encoding/json"

// 消息角色 workbench_messages.role（§4.7）。
// 只有 user 与 assistant 两个值：本表只存给人看的对话，工具调用不进本表（§4.9）。
const (
	WorkbenchMessageRoleUser      = "user"
	WorkbenchMessageRoleAssistant = "assistant"
)

// 消息内容格式 workbench_messages.content_format（§4.7）。
const (
	WorkbenchMessageFormatMarkdown  = "markdown"
	WorkbenchMessageFormatPlainText = "plain_text"
)

// 消息状态 workbench_messages.status（§4.7）。
// 没有 streaming：流式过程中不落库，吐完了才插，所以库里不存在中间态。
const (
	WorkbenchMessageStatusComplete = "complete"
	WorkbenchMessageStatusFailed   = "failed"
)

// WorkbenchMessage 工作台消息，对应表 workbench_messages（设计文档 §4.7）。
//
// 本表只存给人看的对话。assistant 消息落库前必须把 tool_calls 剥掉，Content 只存纯文本——
// ContentFormat 声明了是 markdown/plain_text，存进去的就得真的是那个格式，不能是
// {"text": ..., "tool_calls": [...]} 这种混合体。模型「先输出空文本 + tool_calls、等工具
// 结果回来再说话」的中间态也不落库，只落最终那条有文本的消息，否则表里会堆一串空行。
//
// 代价是 Agent 拼上下文时看不到自己上一轮调过什么。这里认了：工具结果的权威副本在别处
// （解析结果在素材模块、课件改动在 scenes），而且几万字的工具结果进了上下文照样会被滚动
// 摘要压掉（§4.6），落一份换不到什么。
//
// 写入时机：流式过程中不落库，成功落一条 complete，失败落一条 failed（Content 存已经吐
// 出来的那部分，前端刷新后仍能显示「生成失败，点击重试」）。不采用「先插空行、边流边更新」
// 的写法，所以库里不存在 streaming 中间态，也就没有崩溃后残留的半截消息需要清理。
//
// 消息在会话内按 Sequence 严格递增。UNIQUE (session_id, sequence) 与 Role、ContentFormat、
// Status 三个 CHECK 均写在 SQL migration 中（§7）。
type WorkbenchMessage struct {
	BaseModel

	SessionID uint64 `gorm:"column:session_id;not null" json:"session_id"`      // 所属会话；级联删除
	Sequence  int32  `gorm:"column:sequence;not null" json:"sequence"`          // 会话内严格递增的消息序号，> 0
	Role      string `gorm:"column:role;type:varchar(32);not null" json:"role"` // user | assistant

	Content       string `gorm:"column:content;type:text;not null;default:''" json:"content"`                              // 文本内容
	ContentFormat string `gorm:"column:content_format;type:varchar(32);not null;default:'markdown'" json:"content_format"` // markdown | plain_text
	Status        string `gorm:"column:status;type:varchar(32);not null;default:'complete'" json:"status"`                 // complete | failed

	// Metadata 预留的扩展位，写入时留空对象即可。设计意图是放模型、token、引用来源这类
	// 附加信息，但 V1 前端不展示模型名、不显示 token 用量、也没有引用 UI，暂时没有消费方。
	// 用 jsonb 是为了以后往里加 key 时不必改表结构。
	Metadata json.RawMessage `gorm:"column:metadata;type:jsonb;not null;default:'{}'" json:"metadata"`
}

// TableName 返回表名。
func (WorkbenchMessage) TableName() string { return "workbench_messages" }
