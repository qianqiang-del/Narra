package entity

import "time"

const (
	MemoryScopeConversation = "conversation"
	MemoryScopeClassroom    = "classroom"

	MemoryTypeFact          = "fact"
	MemoryTypeDecision      = "decision"
	MemoryTypeLearningState = "learning_state"
	MemoryTypePreference    = "preference"
	MemoryTypeOpenQuestion  = "open_question"

	MemoryStatusActive     = "active"
	MemoryStatusSuperseded = "superseded"
	MemoryStatusRetracted  = "retracted"
)

// SharedContextMemory 是同一课堂内所有 Agent 可读取的结构化记忆。
//
// 约束全部由字段上的 tag 声明，AutoMigrate 建：五个 CHECK、五条外键
// （课堂与会话级联删除，来源消息/回合/取代者置 NULL，取代者是自引用）。
// 两个复合索引的列序与字段声明顺序不一致，所以用显式 priority 固定：
// idx_shared_memories_classroom (classroom_id, scope, status, importance DESC)、
// idx_shared_memories_conversation (conversation_id, status, importance DESC)。
type SharedContextMemory struct {
	BaseModel

	ClassroomID     uint64     `gorm:"column:classroom_id;not null;index:idx_shared_memories_classroom,priority:1;comment:所属课程 ID，指向 classrooms.id；课程删除时级联删除" json:"classroom_id"`
	ConversationID  *uint64    `gorm:"column:conversation_id;index:idx_shared_memories_conversation,priority:1;comment:所属会话 ID；scope 为 conversation 时必填，为 classroom 时必须为空" json:"conversation_id"`
	Scope           string     `gorm:"column:scope;type:varchar(16);not null;index:idx_shared_memories_classroom,priority:2;check:shared_context_memories_scope_check,(scope = 'classroom' AND conversation_id IS NULL) OR (scope = 'conversation' AND conversation_id IS NOT NULL);comment:记忆作用域，取值 classroom（整堂课共享）/ conversation（只在某个会话里有效）" json:"scope"`
	MemoryType      string     `gorm:"column:memory_type;type:varchar(32);not null;check:shared_context_memories_type_check,memory_type IN ('fact', 'decision', 'learning_state', 'preference', 'open_question');comment:记忆类型，取值 fact（事实）/ decision（决定）/ learning_state（学习状态）/ preference（偏好）/ open_question（待解问题）" json:"memory_type"`
	Content         string     `gorm:"column:content;type:text;not null;comment:记忆正文" json:"content"`
	Importance      int16      `gorm:"column:importance;not null;default:3;index:idx_shared_memories_classroom,priority:4,sort:DESC;index:idx_shared_memories_conversation,priority:3,sort:DESC;check:shared_context_memories_importance_check,importance BETWEEN 1 AND 5;comment:重要度，1 ~ 5，越大越优先被召回进上下文" json:"importance"`
	SourceMessageID *uint64    `gorm:"column:source_message_id;comment:这条记忆从哪条消息提炼而来；来源消息被删除后置空" json:"source_message_id"`
	SourceTurnID    *uint64    `gorm:"column:source_turn_id;comment:这条记忆从哪个 Agent 回合提炼而来；回合被删除后置空" json:"source_turn_id"`
	Status          string     `gorm:"column:status;type:varchar(16);not null;default:active;index:idx_shared_memories_classroom,priority:3;index:idx_shared_memories_conversation,priority:2;check:shared_context_memories_status_check,status IN ('active', 'superseded', 'retracted');comment:记忆状态，取值 active（生效）/ superseded（被新记忆取代）/ retracted（已撤回）" json:"status"`
	SupersededByID  *uint64    `gorm:"column:superseded_by_id;check:shared_context_memories_not_self_superseded_check,superseded_by_id IS NULL OR superseded_by_id <> id;comment:取代它的那条记忆 ID，自引用；不允许指向自己" json:"superseded_by_id"`
	ExpiresAt       *time.Time `gorm:"column:expires_at;comment:过期时间；为空表示不过期" json:"expires_at"`
	LastUsedAt      *time.Time `gorm:"column:last_used_at;comment:最近一次被召回进上下文的时间；为空表示还没用过" json:"last_used_at"`

	// 以下关联仅供 AutoMigrate 建外键（CASCADE / CASCADE / SET NULL / SET NULL / 自引用 SET NULL）。
	// 业务代码禁止给它们赋值或 Preload。
	Classroom     *Classroom             `gorm:"foreignKey:ClassroomID;constraint:shared_context_memories_classroom_id_fkey,OnDelete:CASCADE" json:"-"`
	Conversation  *ClassroomConversation `gorm:"foreignKey:ConversationID;constraint:shared_context_memories_conversation_id_fkey,OnDelete:CASCADE" json:"-"`
	SourceMessage *ConversationMessage   `gorm:"foreignKey:SourceMessageID;constraint:shared_context_memories_source_message_id_fkey,OnDelete:SET NULL" json:"-"`
	SourceTurn    *AgentTurn             `gorm:"foreignKey:SourceTurnID;constraint:shared_context_memories_source_turn_id_fkey,OnDelete:SET NULL" json:"-"`
	SupersededBy  *SharedContextMemory   `gorm:"foreignKey:SupersededByID;constraint:shared_context_memories_superseded_by_id_fkey,OnDelete:SET NULL" json:"-"`
}

func (SharedContextMemory) TableName() string { return "shared_context_memories" }
