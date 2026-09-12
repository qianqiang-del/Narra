package entity

import "encoding/json"

// 消息角色 workbench_messages.role（§4.8）。
const (
	WorkbenchMessageRoleSystem    = "system"
	WorkbenchMessageRoleUser      = "user"
	WorkbenchMessageRoleAssistant = "assistant"
	WorkbenchMessageRoleTool      = "tool"
)

// 消息内容格式 workbench_messages.content_format（§4.8）。
const (
	WorkbenchMessageFormatMarkdown  = "markdown"
	WorkbenchMessageFormatPlainText = "plain_text"
	WorkbenchMessageFormatJSON      = "json"
)

// 消息状态 workbench_messages.status（§4.8）。
const (
	WorkbenchMessageStatusStreaming = "streaming"
	WorkbenchMessageStatusComplete  = "complete"
	WorkbenchMessageStatusFailed    = "failed"
)

// WorkbenchMessage 工作台消息，对应表 workbench_messages（设计文档 §4.8）。
//
// 消息在会话内按 Sequence 严格递增，UNIQUE (session_id, sequence) 写在 SQL migration 中（§7）。
//
// 本表的 id 还被 scene_backups.batch_id 复用：它标识「触发本次课件修改的那条用户消息」，
// 因此一条用户消息即使触发多轮工具调用也只算一个撤销批次（§4.10）。
type WorkbenchMessage struct {
	BaseModel

	SessionID uint64 `gorm:"column:session_id;not null" json:"session_id"`      // 所属会话；级联删除
	Sequence  int32  `gorm:"column:sequence;not null" json:"sequence"`          // 会话内严格递增的消息序号，> 0
	Role      string `gorm:"column:role;type:varchar(32);not null" json:"role"` // system | user | assistant | tool

	Content       string `gorm:"column:content;type:text;not null;default:''" json:"content"`                              // 文本内容
	ContentFormat string `gorm:"column:content_format;type:varchar(32);not null;default:'markdown'" json:"content_format"` // markdown | plain_text | json
	Status        string `gorm:"column:status;type:varchar(32);not null;default:'complete'" json:"status"`                 // streaming | complete | failed

	// Metadata 模型、token、引用来源等。
	Metadata json.RawMessage `gorm:"column:metadata;type:jsonb;not null;default:'{}'" json:"metadata"`
}

// TableName 返回表名。
func (WorkbenchMessage) TableName() string { return "workbench_messages" }
