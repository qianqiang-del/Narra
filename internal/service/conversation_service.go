package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"gorm.io/gorm"

	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
)

// conversationFinder 是本服务对对话存储的最小依赖面。
//
// 只要 FindByID：事件流只关心"这条对话在不在"，对话的创建与关闭是别的链路的事。
type conversationFinder interface {
	FindByID(ctx context.Context, id uint64) (*entity.ClassroomConversation, error)
}

// conversationEventReader 是本服务对事件存储的最小依赖面。
//
// 服务层不碰 AppendNext：事件的写入属于生产者（编排 / 工作台）的事务，
// 这里只读 —— 与 SSE 端点的职责一致（查询、序列化、推送）。
type conversationEventReader interface {
	ListAfter(ctx context.Context, conversationID uint64, after int64, limit int) ([]entity.ConversationEvent, error)
}

type conversationService struct {
	conversations conversationFinder
	events        conversationEventReader
}

var _ ConversationService = (*conversationService)(nil)

// NewConversationService 创建对话服务。
func NewConversationService(conversations conversationFinder, events conversationEventReader) ConversationService {
	return &conversationService{conversations: conversations, events: events}
}

// Exists 判断对话是否存在。
//
// 用一次主键查询而不是 Count：事件流紧接着就要开流，这里只是把"对话不存在"
// 挡在响应头之前（响应头一出去，错误就只剩事件流一种表达方式了）。
func (s *conversationService) Exists(ctx context.Context, id uint64) (bool, error) {
	_, err := s.conversations.FindByID(ctx, id)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return false, nil
	default:
		return false, fmt.Errorf("查询对话失败: %w", err)
	}
}

// ListEventsAfter 增量取事件并翻成对外结构。
func (s *conversationService) ListEventsAfter(
	ctx context.Context,
	conversationID uint64,
	after int64,
	limit int,
) ([]responsedto.ConversationEvent, error) {
	events, err := s.events.ListAfter(ctx, conversationID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("查询对话事件失败: %w", err)
	}

	out := make([]responsedto.ConversationEvent, len(events))
	for index, event := range events {
		out[index] = toConversationEventResponse(event)
	}
	return out, nil
}

// toConversationEventResponse 把实体翻成对外结构。
//
// payload 为空时补一个空对象：列上有 default '{}'，正常不会为空，
// 但 JSON 里 `null` 会让前端多写一处判空，这里统一成对象。
func toConversationEventResponse(event entity.ConversationEvent) responsedto.ConversationEvent {
	payload := event.Payload
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}
	return responsedto.ConversationEvent{
		SequenceNo: event.SequenceNo,
		EventType:  event.EventType,
		RunID:      event.RunID,
		TurnID:     event.TurnID,
		Payload:    payload,
		CreatedAt:  event.CreatedAt,
	}
}
