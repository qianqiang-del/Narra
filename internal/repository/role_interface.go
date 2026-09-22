package repository

import (
	"context"

	"narra/internal/model/entity"
)

// RoleRepository 角色池的读取。
type RoleRepository interface {
	// ListEnabled 返回所有未下架的角色，按 sort_order 升序。
	//
	// 过滤与排序放在这里而不是业务层：让数据库干活，也别把已经下架的角色捞进内存再丢。
	ListEnabled(ctx context.Context) ([]entity.PresetAgent, error)

	// ListByIDs 按 ID 批量读取角色，按 sort_order 升序。
	//
	// 刻意不过滤 enabled：已生成的课程可能引用了后来被下架的角色，那时仍要显示出来。
	ListByIDs(ctx context.Context, ids []uint64) ([]entity.PresetAgent, error)
}
