package service

import "context"

// RoleService 角色池业务。
type RoleService interface {
	// List 返回可挑选的角色，按展示顺序排列。
	List(ctx context.Context) ([]RoleItem, error)
}
