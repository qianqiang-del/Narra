package service

import (
	"context"

	responsedto "narra/internal/model/dto/response"
)

type SceneService interface {
	ListNarration(ctx context.Context, sceneID uint64) ([]responsedto.SceneNarrationSegment, error)
	GetContent(ctx context.Context, sceneID uint64) (*responsedto.SceneContentResponse, error)
	Get(ctx context.Context, sceneID uint64) (*responsedto.SceneDetailResponse, error)
}
