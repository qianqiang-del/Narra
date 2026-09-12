package entity

import (
	"encoding/json"

	"gorm.io/gorm"
)

// 会话状态 workbench_sessions.status（§4.7）。
const (
	WorkbenchSessionStatusActive   = "active"
	WorkbenchSessionStatusArchived = "archived"
)

// WorkbenchSession 工作台会话，对应表 workbench_sessions（设计文档 §4.7）。
//
// 工作台以课程为入口：会话必须归属一门课程，不存在任何无课程的会话；进入工作台首先
// 选择课程，选定后默认开一个新会话，历史会话按课程分组查看。
//
// ClassroomID 非空且为 CASCADE 而非 SET NULL——课程删除时会话一并删除，不保留孤儿会话（§6）。
// 会话在用户发出第一条消息时才落库；进入课程后未发言就离开不会产生空会话。
//
// 本表是 V1 唯一需要软删除的主实体（§3），DeletedAt 由 GORM 软删除接管。
//
// 工作台不提供联网搜索：可用能力只有上传素材解析与预置 skill。skill 是代码中的常量集合
// （如「精简讲解」「补充练习」），不建表，只在 AgentConfig 中记录本次使用的 skill 标识。
//
// 注意：本表与 workbench_messages、session_memories、scene_backups 按 §10 第 12 项
// 本轮只设计、不立即建表，迁移脚本在后续迭代生成。
type WorkbenchSession struct {
	BaseModel

	ClassroomID uint64 `gorm:"column:classroom_id;not null" json:"classroom_id"`      // 所属课程；非空、级联删除
	Title       string `gorm:"column:title;type:varchar(200);not null" json:"title"`  // 会话标题
	Status      string `gorm:"column:status;type:varchar(32);not null" json:"status"` // active | archived

	// AgentConfig 工作台 Agent 配置快照，记录本次会话使用的 skill 标识等。
	AgentConfig json.RawMessage `gorm:"column:agent_config;type:jsonb;not null;default:'{}'" json:"agent_config"`

	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at" json:"-"` // 软删除时间
}

// TableName 返回表名。
func (WorkbenchSession) TableName() string { return "workbench_sessions" }
