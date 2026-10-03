package service

import (
	"context"

	responsedto "narra/internal/model/dto/response"
)

type TraceService interface {
	ListRuns(ctx context.Context, conversationID uint64, limit int) ([]responsedto.DiscussionRun, error)
	GetTrace(ctx context.Context, conversationID, runID uint64) (*responsedto.DiscussionTrace, error)
}
