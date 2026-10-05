package repository

import (
	"context"
	"encoding/json"
	"time"

	"narra/internal/model/entity"
)

// ClassroomSceneStat 是课堂列表按课汇总的场景统计。
type ClassroomSceneStat struct {
	// Pages 是这门课的内容页数，不含代码在计划末尾追加的课程完成页。
	Pages int64
	// ReadyPages 是其中已生成好的页数，决定这门课点进去是直接进课堂还是先去生成页等。
	ReadyPages int64
}

type SceneRepository interface {
	FindByID(ctx context.Context, id uint64) (*entity.Scene, error)
	CreateBatch(ctx context.Context, scenes []*entity.Scene) error
	DeleteByClassroom(ctx context.Context, classroomID uint64) error
	ListByClassroom(ctx context.Context, classroomID uint64) ([]entity.Scene, error)
	CountSceneStatsByClassrooms(ctx context.Context, classroomIDs []uint64) (map[uint64]ClassroomSceneStat, error)
	ListFirstByClassrooms(ctx context.Context, classroomIDs []uint64) (map[uint64]entity.Scene, error)
	UpdateContent(ctx context.Context, id uint64, owner string, content, review json.RawMessage, interactiveHTML string) error
	UpdateCheckpoint(ctx context.Context, id uint64, owner string, checkpoint json.RawMessage) error
	ClearCheckpoint(ctx context.Context, id uint64, owner string) error
	CompleteGeneration(ctx context.Context, id uint64, owner string) error
	UpdatePhase(ctx context.Context, id uint64, owner string, phase string) error
	UpdateStatus(ctx context.Context, id uint64, owner string, status string, errorMessage *string) error
	ResetForRetry(ctx context.Context, id uint64) (bool, error)
	RestoreRetryFailure(ctx context.Context, id uint64, message string) error
	AcquireLease(ctx context.Context, id uint64, owner, runID string, ttl time.Duration) (bool, error)
	RenewLease(ctx context.Context, id uint64, owner string, ttl time.Duration) (bool, error)
	ReleaseLease(ctx context.Context, id uint64, owner string) error
}
