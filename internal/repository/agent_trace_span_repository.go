package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

type agentTraceSpanRepository struct {
	db *gorm.DB
}

func NewAgentTraceSpanRepository(db *gorm.DB) AgentTraceSpanRepository {
	return &agentTraceSpanRepository{db: db}
}

func (r *agentTraceSpanRepository) Create(ctx context.Context, span *entity.AgentTraceSpan) error {
	return conn(ctx, r.db).Create(span).Error
}

func (r *agentTraceSpanRepository) ListByRun(ctx context.Context, runID uint64) ([]entity.AgentTraceSpan, error) {
	var spans []entity.AgentTraceSpan
	err := conn(ctx, r.db).Where("run_id = ?", runID).Order("started_at ASC, id ASC").Find(&spans).Error
	return spans, err
}

func (r *agentTraceSpanRepository) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	result := conn(ctx, r.db).Where("expires_at < ?", before).Delete(&entity.AgentTraceSpan{})
	return result.RowsAffected, result.Error
}
