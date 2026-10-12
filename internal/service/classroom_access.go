package service

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"narra/internal/ownership"
	"narra/internal/repository"
	apperrors "narra/pkg/errors"
)

func ensureClassroomAccess(ctx context.Context, classrooms repository.ClassroomRepository, classroomID uint64) error {
	ownerID := ownership.FromContext(ctx)
	if ownerID == 0 {
		// Background jobs and legacy service callers have no HTTP identity.
		return nil
	}
	_, err := classrooms.FindByIDAndOwner(ctx, classroomID, ownerID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperrors.New(apperrors.CodeNotFound, "课堂不存在")
	}
	if err != nil {
		return apperrors.NewWithErr(apperrors.CodeInternalError, "查询课堂失败", err)
	}
	return nil
}

func ensureConversationAccess(ctx context.Context, conversations repository.ConversationRepository, classrooms repository.ClassroomRepository, conversationID uint64) error {
	conversation, err := conversations.FindByID(ctx, conversationID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperrors.New(apperrors.CodeNotFound, "对话不存在")
	}
	if err != nil {
		return apperrors.NewWithErr(apperrors.CodeInternalError, "查询对话失败", err)
	}
	return ensureClassroomAccess(ctx, classrooms, conversation.ClassroomID)
}
