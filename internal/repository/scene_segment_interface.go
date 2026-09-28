package repository

import (
	"context"

	"narra/internal/model/entity"
)

// SceneSegmentRepository 是讲解段落的读写口。
//
// 两个写方法都要带 owner：讲解段落本身没有租约，它靠所属页面的租约保护。
// 页面租约易主后，老执行者连音频都不该再写——同一 content_key 的行会被新内容复用，
// 迟到的音频会挂到已经改过的讲稿上。
type SceneSegmentRepository interface {
	ListByScene(ctx context.Context, sceneID uint64) ([]entity.SceneSegment, error)
	UpdateAudio(ctx context.Context, sceneID, id uint64, owner string, audioPath string, status string) error
	UpdateStatus(ctx context.Context, sceneID, id uint64, owner string, status string, errorMessage *string) error
	ReplaceByScene(ctx context.Context, sceneID uint64, segments []*entity.SceneSegment) error
}
