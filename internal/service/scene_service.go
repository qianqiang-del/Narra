package service

import (
	"context"

	responsedto "narra/internal/model/dto/response"
	"narra/internal/repository"
	apperrors "narra/pkg/errors"
)

type sceneService struct {
	segments repository.SceneSegmentRepository
	scenes   repository.SceneRepository
}

func NewSceneService(segments repository.SceneSegmentRepository, scenes repository.SceneRepository) SceneService {
	return &sceneService{segments: segments, scenes: scenes}
}

func (s *sceneService) GetContent(ctx context.Context, sceneID uint64) (*responsedto.SceneContentResponse, error) {
	scene, err := s.scenes.FindByID(ctx, sceneID)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeNotFound, "场景不存在", err)
	}
	return &responsedto.SceneContentResponse{SceneID: scene.ID, Status: scene.Status, Content: scene.Content}, nil
}

func (s *sceneService) Get(ctx context.Context, sceneID uint64) (*responsedto.SceneDetailResponse, error) {
	scene, err := s.scenes.FindByID(ctx, sceneID)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeNotFound, "场景不存在", err)
	}
	segments, err := s.segments.ListByScene(ctx, sceneID)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询讲解稿失败", err)
	}
	narration := make([]responsedto.SceneNarrationSegment, 0, len(segments))
	for _, segment := range segments {
		narration = append(narration, responsedto.SceneNarrationSegment{ID: segment.ID, SceneID: segment.SceneID, ContentKey: segment.ContentKey, SortOrder: segment.SortOrder, Text: segment.Text, Status: segment.Status, AudioPath: segment.AudioPath})
	}
	return &responsedto.SceneDetailResponse{ID: scene.ID, SortOrder: scene.SortOrder, Type: scene.Type, Title: scene.Title, Brief: scene.Brief, Status: scene.Status, Content: scene.Content, Narration: narration, ErrorMessage: scene.ErrorMessage}, nil
}

func (s *sceneService) ListNarration(ctx context.Context, sceneID uint64) ([]responsedto.SceneNarrationSegment, error) {
	segments, err := s.segments.ListByScene(ctx, sceneID)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询讲解稿失败", err)
	}
	items := make([]responsedto.SceneNarrationSegment, 0, len(segments))
	for _, segment := range segments {
		items = append(items, responsedto.SceneNarrationSegment{
			ID: segment.ID, SceneID: segment.SceneID, ContentKey: segment.ContentKey,
			SortOrder: segment.SortOrder, Text: segment.Text, Status: segment.Status,
			AudioPath: segment.AudioPath,
		})
	}
	return items, nil
}
