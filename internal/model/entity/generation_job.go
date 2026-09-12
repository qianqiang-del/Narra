package entity

import (
	"encoding/json"
	"time"
)

// 任务类型 generation_jobs.kind（§4.6）。
const (
	GenerationJobKindClassroomGeneration = "classroom_generation"
	GenerationJobKindRegenerateScene     = "regenerate_scene"
)

// 任务状态 generation_jobs.status（§5.3）。
const (
	GenerationJobStatusQueued    = "queued"    // 已创建，等待执行
	GenerationJobStatusRunning   = "running"   // 正在执行
	GenerationJobStatusSucceeded = "succeeded" // 成功完成
	GenerationJobStatusFailed    = "failed"    // 失败
	GenerationJobStatusCancelled = "cancelled" // 用户取消
)

// 任务阶段 generation_jobs.current_phase（§4.6）。
const (
	GenerationJobPhaseOutline        = "outline"
	GenerationJobPhaseSceneContent   = "scene_content"
	GenerationJobPhaseSceneNarration = "scene_narration"
	GenerationJobPhaseCompleted      = "completed"
)

// GenerationJob 课程生成任务，对应表 generation_jobs（设计文档 §4.6）。
//
// 一个课程同一时间只允许一个运行中的生成任务；该规则由事务/应用锁保证，
// 如需数据库强约束可增加针对 status IN ('queued', 'running') 的部分唯一索引。
type GenerationJob struct {
	BaseModel

	ClassroomID uint64  `gorm:"column:classroom_id;not null" json:"classroom_id"` // 所属课程；级联删除
	ParentJobID *uint64 `gorm:"column:parent_job_id" json:"parent_job_id"`        // 重试任务指向原任务，可空

	Kind   string `gorm:"column:kind;type:varchar(32);not null" json:"kind"`     // classroom_generation | regenerate_scene
	Status string `gorm:"column:status;type:varchar(32);not null" json:"status"` // 任务状态，见 §5.3

	CurrentSceneID *uint64 `gorm:"column:current_scene_id" json:"current_scene_id"`            // 当前正在生成的场景；生成大纲阶段为空
	CurrentPhase   *string `gorm:"column:current_phase;type:varchar(32)" json:"current_phase"` // outline | scene_content | scene_narration | completed

	RequestConfig json.RawMessage `gorm:"column:request_config;type:jsonb;not null;default:'{}'" json:"request_config"` // 此次任务的完整输入快照
	ResultSummary json.RawMessage `gorm:"column:result_summary;type:jsonb;not null;default:'{}'" json:"result_summary"` // 场景数、token 用量、模型响应 ID 等摘要

	ErrorCode    *string    `gorm:"column:error_code;type:varchar(64)" json:"error_code"` // 业务错误码
	ErrorMessage *string    `gorm:"column:error_message;type:text" json:"error_message"`  // 失败摘要，禁止写入密钥
	StartedAt    *time.Time `gorm:"column:started_at" json:"started_at"`                  // 开始时间
	FinishedAt   *time.Time `gorm:"column:finished_at" json:"finished_at"`                // 结束时间
}

// TableName 返回表名。
func (GenerationJob) TableName() string { return "generation_jobs" }
