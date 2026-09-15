package service

import (
	"context"

	dto "narra/internal/model/dto/response"
	"narra/internal/repository"
	"narra/pkg/errors"
)

// roleService 角色池业务实现。
type roleService struct {
	repo repository.RoleRepository
}

// NewRoleService 创建角色池业务服务。
func NewRoleService(repo repository.RoleRepository) RoleService {
	return &roleService{repo: repo}
}

// List 查可挑选的角色，转成对外结构。
func (s *roleService) List(ctx context.Context) ([]dto.RoleItem, error) {
	agents, err := s.repo.ListEnabled(ctx)
	if err != nil {
		// 原始错误带进 BizError，让它能被日志捞到；对外的 Message 是给人看的。
		return nil, errors.NewWithErr(errors.CodeInternalError, "查询角色列表失败", err)
	}

	// 用 make 而不是 var：空结果要序列化成 [] 而不是 null，
	// 否则前端 .map() 会炸在一个看起来像"没有数据"的 null 上。
	items := make([]dto.RoleItem, 0, len(agents))
	for _, a := range agents {
		items = append(items, dto.RoleItem{
			AgentKey:  a.AgentKey,
			Name:      a.Name,
			Role:      a.Role,
			RoleType:  a.RoleType,
			Persona:   a.Persona,
			Avatar:    a.Avatar,
			Color:     a.Color,
			VoiceID:   a.VoiceID,
			SortOrder: a.SortOrder,
		})
	}

	return items, nil
}
