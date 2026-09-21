package entity

import (
	"encoding/json"
	"time"
)

const (
	RunStatusQueued      = "queued"
	RunStatusRunning     = "running"
	RunStatusWaitingUser = "waiting_user"
	RunStatusCompleted   = "completed"
	RunStatusFailed      = "failed"
	RunStatusCancelled   = "cancelled"

	RunStopCompleted = "completed"
	RunStopWaiting   = "waiting_user"
	RunStopMaxTurns  = "max_turns"
	RunStopError     = "error"
	RunStopCancelled = "cancelled"
)

// OrchestrationRun 是一条用户消息触发的一次完整多 Agent 编排。
//
// UNIQUE (trace_id) 由 TraceID 上的 unique tag 声明，AutoMigrate 负责建。单列唯一约束不能写进
// SQL migration：AutoMigrate 每次启动都会对账，发现库里唯一而实体上没标 unique，就按自己算的
// 名字 uni_orchestration_runs_trace_id 去删，删不掉直接 panic。见 migrations/README.md。
//
// 其余约束同样全部由 tag 声明：六个 CHECK、UNIQUE (trigger_message_id, attempt_no)、
// 两条外键（conversation 与触发消息均级联删除），以及「按会话查最近编排」的复合索引
// idx_orchestration_runs_recent——它的第二列是 created_at，所以下方遮蔽了
// BaseModel.CreatedAt 来挂索引（遮蔽安全，见 KnowledgeDocument.UpdatedAt 的注释）。
type OrchestrationRun struct {
	BaseModel

	ConversationID      uint64          `gorm:"column:conversation_id;not null;index:idx_orchestration_runs_recent;comment:所属会话 ID，指向 classroom_conversations.id；会话删除时级联删除" json:"conversation_id"`
	TriggerMessageID    uint64          `gorm:"column:trigger_message_id;not null;uniqueIndex:orchestration_runs_trigger_attempt_key;comment:触发这次编排的用户消息 ID，指向 conversation_messages.id；消息删除时级联删除" json:"trigger_message_id"`
	AttemptNo           int16           `gorm:"column:attempt_no;not null;default:1;uniqueIndex:orchestration_runs_trigger_attempt_key;check:orchestration_runs_attempt_no_check,attempt_no >= 1;comment:同一条触发消息的第几次编排，从 1 开始；与 trigger_message_id 组成唯一约束" json:"attempt_no"`
	TraceID             string          `gorm:"column:trace_id;type:varchar(32);not null;unique;check:orchestration_runs_trace_id_format_check,trace_id ~ '^[0-9a-f]{32}$';comment:链路追踪 ID，32 位小写十六进制；全局唯一，把这条链路上所有 span 串起来" json:"trace_id"`
	Status              string          `gorm:"column:status;type:varchar(32);not null;check:orchestration_runs_status_check,status IN ('queued', 'running', 'waiting_user', 'completed', 'failed', 'cancelled');comment:运行状态，取值 queued（排队）/ running（执行中）/ waiting_user（等用户输入）/ completed（完成）/ failed（失败）/ cancelled（取消）" json:"status"`
	MaxTurns            int16           `gorm:"column:max_turns;not null;check:orchestration_runs_max_turns_check,max_turns BETWEEN 1 AND 50;comment:本次运行最多允许的回合数，取值 1 ~ 50" json:"max_turns"`
	StopReason          *string         `gorm:"column:stop_reason;type:varchar(32);check:orchestration_runs_stop_reason_check,stop_reason IS NULL OR stop_reason IN ('completed', 'waiting_user', 'max_turns', 'error', 'cancelled');comment:运行停止的原因，取值 completed / waiting_user / max_turns / error / cancelled" json:"stop_reason"`
	OrchestratorVersion string          `gorm:"column:orchestrator_version;type:varchar(40);not null;comment:编排器版本号，用于回溯这次是哪一版逻辑跑的" json:"orchestrator_version"`
	ConfigSnapshot      json.RawMessage `gorm:"column:config_snapshot;type:jsonb;not null;default:'{}';comment:本次运行使用的配置快照 JSON，后续改配置不影响已跑过的历史" json:"config_snapshot"`
	StartedAt           *time.Time      `gorm:"column:started_at;check:orchestration_runs_time_range_check,finished_at IS NULL OR started_at IS NULL OR finished_at >= started_at;comment:运行开始时间；未开始时为空" json:"started_at"`
	FinishedAt          *time.Time      `gorm:"column:finished_at;comment:运行结束时间；未结束时为空，且必须不早于 started_at" json:"finished_at"`
	ErrorMessage        *string         `gorm:"column:error_message;type:text;comment:运行失败的摘要" json:"error_message"`

	// CreatedAt 遮蔽 BaseModel 的同名字段，只为给 idx_orchestration_runs_recent 挂第二列。
	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime;index:idx_orchestration_runs_recent,sort:DESC;comment:创建时间，timestamptz 按 UTC 存；本列是「按会话查最近编排」索引的第二列" json:"created_at"`

	// Conversation / TriggerMessage 仅供 AutoMigrate 建外键（均 ON DELETE CASCADE）。
	// 业务代码禁止给它们赋值或 Preload。
	Conversation   *ClassroomConversation `gorm:"foreignKey:ConversationID;constraint:orchestration_runs_conversation_id_fkey,OnDelete:CASCADE" json:"-"`
	TriggerMessage *ConversationMessage   `gorm:"foreignKey:TriggerMessageID;constraint:orchestration_runs_trigger_message_id_fkey,OnDelete:CASCADE" json:"-"`
}

func (OrchestrationRun) TableName() string { return "orchestration_runs" }
