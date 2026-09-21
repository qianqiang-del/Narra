package entity

import "time"

// BaseModel 是所有业务实体的公共字段，对应设计文档 §3「通用约定」。
//
// 主键为 bigint + GENERATED ALWAYS AS IDENTITY（bigserial 语义），Go 侧使用 uint64；
// 时间列统一为 timestamptz，存 UTC。
//
// 注意：tag 中的 autoIncrement 仅供 AutoMigrate 参考，PostgreSQL 侧实际生成的是 bigserial。
// 表结构（含主键 DDL）全部由 AutoMigrate 从实体 tag 建出，migrations/ 不含结构 DDL。
//
// 若某个实体的主键不是自增 id（例如以别的表的主键兼作本表主键），就不能嵌入本结构，
// 而且必须显式写 autoIncrement:false —— GORM 只要发现主键是整数类型、tag 里又没出现
// autoIncrement 这个 key，就会无条件把它当成自增（schema.go 的 PrioritizedPrimaryField 分支），迁移时给这个列建出一个多余的序列。
type BaseModel struct {
	ID        uint64    `gorm:"column:id;primaryKey;autoIncrement;comment:主键，bigint 自增（库内是 GENERATED ALWAYS AS IDENTITY）" json:"id"` // 主键
	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime;comment:创建时间，timestamptz，按 UTC 存" json:"created_at"`        // 创建时间
	UpdatedAt time.Time `gorm:"column:updated_at;not null;autoUpdateTime;comment:最后修改时间，timestamptz，按 UTC 存" json:"updated_at"`      // 更新时间
}
