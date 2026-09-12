package entity

import "time"

// BaseModel 是所有业务实体的公共字段，对应设计文档 §3「通用约定」。
//
// 主键为 bigint + GENERATED ALWAYS AS IDENTITY（bigserial 语义），Go 侧使用 uint64；
// 时间列统一为 timestamptz，存 UTC。
//
// 注意：tag 中的 autoIncrement 仅供 AutoMigrate 参考，PostgreSQL 侧实际生成的是 bigserial。
// 本项目按 §8 用 SQL migration 建表，主键 DDL 以迁移脚本为准。
//
// scene_backups 不嵌入本结构：它的主键是 scene_id 而非自增 id（§4.10）。
type BaseModel struct {
	ID        uint64    `gorm:"column:id;primaryKey;autoIncrement" json:"id"`                // 主键
	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime" json:"created_at"` // 创建时间
	UpdatedAt time.Time `gorm:"column:updated_at;not null;autoUpdateTime" json:"updated_at"` // 更新时间
}
