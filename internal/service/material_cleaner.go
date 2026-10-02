package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"narra/internal/model/entity"
	"narra/pkg/logger"
)

// material_cleaner.go 是课程材料的到期清理目标，挂进 internal/retention 的周期循环。
//
// 材料生命周期：上传（kind=material + expires_at）→ 建课（expires_at 置空、长期保留）
// → 删课（expires_at 设回当前时间、回到待清理）。这里只负责最后一步：
// 把 expires_at 已过、且没有被课堂关联的材料删掉。
//
// 为什么不直接 DELETE 一行：文档删除要连带清理失败原件的归档目录与文档图片，
// 这些磁盘动作都在 KnowledgeService.Delete 里（切片与向量则靠外键级联）。
// 名单只是快照 —— 查询与删除之间材料可能刚好被建课关联，所以删之前必须重读确认。

// expiredMaterialStore 是到期材料清理对知识库仓储的最小依赖面。
type expiredMaterialStore interface {
	// ListExpiredMaterials 取 expires_at < before 的课程材料 ID（升序）。
	ListExpiredMaterials(ctx context.Context, before time.Time) ([]uint64, error)
	// GetByID 在删除前重读一行，确认它仍是到期未关联的材料。
	GetByID(ctx context.Context, id uint64) (*entity.KnowledgeDocument, error)
}

// materialDeleter 删除一篇文档；*knowledgeService 满足它。
type materialDeleter interface {
	Delete(ctx context.Context, id uint64) error
}

// ExpiredMaterialCleaner 实现 retention.Expirer：一次扫一批到期材料并逐篇删除。
type ExpiredMaterialCleaner struct {
	store   expiredMaterialStore
	deleter materialDeleter
}

// NewExpiredMaterialCleaner 构造清理器；两个依赖缺一不可，装配期就报错。
func NewExpiredMaterialCleaner(store expiredMaterialStore, deleter materialDeleter) (*ExpiredMaterialCleaner, error) {
	if store == nil || deleter == nil {
		return nil, fmt.Errorf("课程材料清理器缺少依赖：store=%v deleter=%v", store != nil, deleter != nil)
	}
	return &ExpiredMaterialCleaner{store: store, deleter: deleter}, nil
}

// DeleteExpired 删除 expires_at 早于 before 的未关联课程材料，返回删除成功数。
//
// 单篇失败只记日志继续：retention 每轮重扫，下一次还会遇到它。
// 重读时发现材料已被关联（expires_at 被清空）或被手动删除，都安静跳过 ——
// 前者绝不能删，后者目的已经达到。
func (c *ExpiredMaterialCleaner) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	ids, err := c.store.ListExpiredMaterials(ctx, before)
	if err != nil {
		return 0, fmt.Errorf("查询到期课程材料失败: %w", err)
	}

	deleted := int64(0)
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		document, err := c.store.GetByID(ctx, id)
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				logger.Warn("读取到期课程材料失败，跳过", zap.Uint64("document_id", id), zap.Error(err))
			}
			continue
		}
		if document.Kind != entity.KnowledgeDocumentKindMaterial ||
			document.ExpiresAt == nil || !document.ExpiresAt.Before(before) {
			// 已被关联（expires_at 清空）或清理时间被推迟：这轮不删。
			continue
		}
		if err := c.deleter.Delete(ctx, id); err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				logger.Warn("删除到期课程材料失败，下一轮重试", zap.Uint64("document_id", id), zap.Error(err))
			}
			continue
		}
		deleted++
		logger.Info("已清理到期课程材料", zap.Uint64("document_id", id))
	}
	return deleted, nil
}
