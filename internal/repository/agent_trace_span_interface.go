package repository

import (
	"context"
	"time"

	"narra/internal/model/entity"
)

// AgentTraceSpanRepository 保存讨论运行的本地追踪步骤。
type AgentTraceSpanRepository interface {
	Create(ctx context.Context, span *entity.AgentTraceSpan) error
	ListByRun(ctx context.Context, runID uint64) ([]entity.AgentTraceSpan, error)
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}
