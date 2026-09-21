package entity

import (
	"encoding/json"
	"time"
)

const (
	AgentTurnStatusScheduled = "scheduled"
	AgentTurnStatusRunning   = "running"
	AgentTurnStatusCompleted = "completed"
	AgentTurnStatusFailed    = "failed"
	AgentTurnStatusCancelled = "cancelled"

	AgentTurnActionContinue    = "continue"
	AgentTurnActionSwitchAgent = "switch_agent"
	AgentTurnActionAskUser     = "ask_user"
	AgentTurnActionEnd         = "end"
)

// AgentTurn 是 Director 选择一个课堂角色后产生的一次 Agent 执行回合。
//
// UNIQUE (output_message_id) 由 OutputMessageID 上的 unique tag 声明；理由同
// OrchestrationRun.TraceID——单列唯一约束归 AutoMigrate，不能写进 SQL。
// 其余约束同样全部由 tag 声明：四个 CHECK、UNIQUE (run_id, turn_no)、
// 三条外键（run 级联删除，课堂角色与产出消息置 NULL）。
type AgentTurn struct {
	BaseModel

	RunID            uint64          `gorm:"column:run_id;not null;uniqueIndex:agent_turns_run_turn_key;comment:所属编排运行 ID，指向 orchestration_runs.id；运行删除时级联删除" json:"run_id"`
	TurnNo           int16           `gorm:"column:turn_no;not null;uniqueIndex:agent_turns_run_turn_key;check:agent_turns_counts_check,turn_no >= 1 AND input_tokens >= 0 AND output_tokens >= 0;comment:该运行内的第几个回合，从 1 开始；与 run_id 组成唯一约束" json:"turn_no"`
	ClassroomAgentID *uint64         `gorm:"column:classroom_agent_id;comment:执行这个回合的课堂角色，指向 classroom_agents.id；角色被移出课程后置空" json:"classroom_agent_id"`
	AgentSnapshot    json.RawMessage `gorm:"column:agent_snapshot;type:jsonb;not null;default:'{}';comment:回合开始时的角色快照 JSON（当时的名字与身份）；角色后来改了名，历史记录显示的仍是当时那份" json:"agent_snapshot"`
	OutputMessageID  *uint64         `gorm:"column:output_message_id;unique;comment:这个回合产出、写进对话的那条消息 ID，指向 conversation_messages.id；全局唯一，一个回合只产出一条消息" json:"output_message_id"`
	Status           string          `gorm:"column:status;type:varchar(32);not null;check:agent_turns_status_check,status IN ('scheduled', 'running', 'completed', 'failed', 'cancelled');comment:回合状态，取值 scheduled（已排期）/ running（执行中）/ completed（完成）/ failed（失败）/ cancelled（取消）" json:"status"`
	SelectionReason  *string         `gorm:"column:selection_reason;type:text;comment:Director 为什么选中这个角色，一段自然语言理由" json:"selection_reason"`
	NextAction       *string         `gorm:"column:next_action;type:varchar(32);check:agent_turns_next_action_check,next_action IS NULL OR next_action IN ('continue', 'switch_agent', 'ask_user', 'end');comment:Director 给出的下一步动作，取值 continue / switch_agent / ask_user / end" json:"next_action"`
	Model            *string         `gorm:"column:model;type:varchar(120);comment:本回合实际调用的模型 ID；未调用模型时为空" json:"model"`
	InputTokens      int32           `gorm:"column:input_tokens;not null;default:0;comment:本回合消耗的输入 token 数；0 表示没有统计" json:"input_tokens"`
	OutputTokens     int32           `gorm:"column:output_tokens;not null;default:0;comment:本回合消耗的输出 token 数；0 表示没有统计" json:"output_tokens"`
	StartedAt        *time.Time      `gorm:"column:started_at;check:agent_turns_time_range_check,finished_at IS NULL OR started_at IS NULL OR finished_at >= started_at;comment:回合开始时间，timestamptz 按 UTC 存；未开始时为空" json:"started_at"`
	FinishedAt       *time.Time      `gorm:"column:finished_at;comment:回合结束时间；未结束时为空，且必须不早于 started_at" json:"finished_at"`
	ErrorMessage     *string         `gorm:"column:error_message;type:text;comment:回合失败的摘要" json:"error_message"`

	// 以下关联仅供 AutoMigrate 建外键（CASCADE / SET NULL / SET NULL）。
	// 业务代码禁止给它们赋值或 Preload。
	Run            *OrchestrationRun    `gorm:"foreignKey:RunID;constraint:agent_turns_run_id_fkey,OnDelete:CASCADE" json:"-"`
	ClassroomAgent *ClassroomAgent      `gorm:"foreignKey:ClassroomAgentID;constraint:agent_turns_classroom_agent_id_fkey,OnDelete:SET NULL" json:"-"`
	OutputMessage  *ConversationMessage `gorm:"foreignKey:OutputMessageID;constraint:agent_turns_output_message_id_fkey,OnDelete:SET NULL" json:"-"`
}

func (AgentTurn) TableName() string { return "agent_turns" }
