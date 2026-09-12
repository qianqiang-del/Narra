package entity

import (
	"encoding/json"
	"time"
)

// SceneBackup 课件修改前备份，对应表 scene_backups（设计文档 §4.10）。
//
// 保存场景「最近一次被修改前」的内容，用于撤销工作台对课件的一次修改。
//
// 每个场景最多保留一行：SceneID 同时作为主键，天然保证一个场景只有一份备份，
// 即只支持撤销最近一次修改；再次修改会覆盖上一份备份，不保留更早版本。
//
// 一次工作台修改会同时涉及多个场景——用户一次可要求改多页，且即使工具是单场景粒度，
// 一条用户消息也可能触发多次调用。BatchID 把它们归为一组：撤销时整批还原，
// 符合用户「撤销刚才那次修改」的心智，而不是逐页撤销。
//
// 本表不嵌入 BaseModel：主键是 scene_id 而非自增 id，且只需要 created_at 一个时间列，
// updated_at 与 deleted_at 在这里没有意义。
type SceneBackup struct {
	// scene_id 是主键但**不是**自增列：它的值由所属场景决定。
	// 必须显式写 autoIncrement:false —— GORM 只要发现主键是整数类型、且 tag 里没出现
	// autoIncrement 这个 key，就会无条件把它当作自增（schema.go 的 PrioritizedPrimaryField
	// 分支），从而在迁移时给 scene_id 建出一个序列，与 §4.10 的要求相反。
	SceneID uint64 `gorm:"column:scene_id;primaryKey;autoIncrement:false" json:"scene_id"` // 所属场景；主键兼外键，级联删除

	// BatchID 修改批次标识，同一次模型修改涉及的所有场景共享同一个值。
	//
	// 它不是行标识，不走 IDENTITY：直接复用触发本次修改的那条用户消息的
	// workbench_messages.id。它本身已是一个 bigint，无需额外生成器，且天然保证
	// 「一条用户消息 → 一个批次」——即使 Agent 分几轮调用工具，只要源自同一条用户消息
	// 就归为同一批。
	BatchID uint64 `gorm:"column:batch_id;not null" json:"batch_id"`

	Title   string          `gorm:"column:title;type:varchar(200);not null" json:"title"` // 修改前的场景标题
	Content json.RawMessage `gorm:"column:content;type:jsonb;not null" json:"content"`    // 修改前的场景内容

	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime" json:"created_at"` // 备份时间
}

// TableName 返回表名。
func (SceneBackup) TableName() string { return "scene_backups" }
