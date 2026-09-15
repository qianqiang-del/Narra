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
}
