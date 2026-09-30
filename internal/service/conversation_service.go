package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
	"narra/internal/repository"
	apperrors "narra/pkg/errors"
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
	conversations repository.ConversationRepository
	classrooms    repository.ClassroomRepository
	messages      repository.MessageRepository
	events        conversationEventReader
}

var _ ConversationService = (*conversationService)(nil)

// NewConversationService 创建对话服务。
func NewConversationService(conversations repository.ConversationRepository, classrooms repository.ClassroomRepository, messages repository.MessageRepository, events conversationEventReader) ConversationService {
	return &conversationService{conversations: conversations, classrooms: classrooms, messages: messages, events: events}
}

const (
	defaultMessageListLimit = 100
	maxMessageListLimit     = 500
)

func (s *conversationService) List(ctx context.Context, classroomID uint64) ([]responsedto.Conversation, error) {
	if err := s.ensureClassroom(ctx, classroomID); err != nil {
		return nil, err
	}
	items, err := s.conversations.ListByClassroom(ctx, classroomID, maxMessageListLimit)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询课堂对话失败", err)
	}
	out := make([]responsedto.Conversation, len(items))
	for index, item := range items {
		out[index] = toConversationResponse(item)
	}
	return out, nil
}

func (s *conversationService) Create(ctx context.Context, classroomID uint64, input requestdto.CreateConversation) (*responsedto.Conversation, error) {
	if err := s.ensureClassroom(ctx, classroomID); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return nil, apperrors.New(apperrors.CodeMissingParam, "对话标题不能为空")
	}
	if utf8.RuneCountInString(title) > 200 {
		return nil, apperrors.New(apperrors.CodeInvalidParam, "对话标题超过 200 字上限")
	}
	if input.Type != entity.ConversationTypeDiscussion {
		return nil, apperrors.New(apperrors.CodeInvalidParam, "当前只支持创建讨论对话")
	}
	conversation := &entity.ClassroomConversation{ClassroomID: classroomID, Title: title, Type: entity.ConversationTypeDiscussion, Status: entity.ConversationStatusActive}
	if err := s.conversations.Create(ctx, conversation); err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "创建对话失败", err)
	}
	out := toConversationResponse(*conversation)
	return &out, nil
}

// Close 结束一条对话。结束后只能查看历史，下一次发言由前端创建新对话。
func (s *conversationService) Close(ctx context.Context, conversationID uint64) error {
	conversation, err := s.findConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if conversation.Status == entity.ConversationStatusClosed {
		return nil
	}
	if err := s.conversations.Close(ctx, conversationID, time.Now().UTC()); err != nil {
		return apperrors.NewWithErr(apperrors.CodeInternalError, "结束对话失败", err)
	}
	return nil
}

func (s *conversationService) ListMessages(ctx context.Context, conversationID uint64, afterSequence int64, limit int) ([]responsedto.ConversationMessage, error) {
	if _, err := s.findConversation(ctx, conversationID); err != nil {
		return nil, err
	}
	items, err := s.messages.ListByConversation(ctx, conversationID, afterSequence, normalizeMessageListLimit(limit))
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询对话消息失败", err)
	}
	out := make([]responsedto.ConversationMessage, len(items))
	for index, item := range items {
		out[index] = toConversationMessageResponse(item)
	}
	return out, nil
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

func (s *conversationService) ensureClassroom(ctx context.Context, classroomID uint64) error {
	_, err := s.classrooms.FindByID(ctx, classroomID)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return apperrors.New(apperrors.CodeNotFound, "课堂不存在")
	default:
		return apperrors.NewWithErr(apperrors.CodeInternalError, "查询课堂失败", err)
	}
}

func (s *conversationService) findConversation(ctx context.Context, conversationID uint64) (*entity.ClassroomConversation, error) {
	conversation, err := s.conversations.FindByID(ctx, conversationID)
	switch {
	case err == nil:
		return conversation, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil, apperrors.New(apperrors.CodeNotFound, "对话不存在")
	default:
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询对话失败", err)
	}
}

func normalizeMessageListLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultMessageListLimit
	case limit > maxMessageListLimit:
		return maxMessageListLimit
	default:
		return limit
	}
}

func toConversationResponse(item entity.ClassroomConversation) responsedto.Conversation {
	return responsedto.Conversation{ID: item.ID, ClassroomID: item.ClassroomID, Title: item.Title, Type: item.Type, Status: item.Status, LastMessageAt: item.LastMessageAt, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

func toConversationMessageResponse(item entity.ConversationMessage) responsedto.ConversationMessage {
	return responsedto.ConversationMessage{ID: item.ID, ConversationID: item.ConversationID, SequenceNo: item.SequenceNo, SenderType: item.SenderType, ClassroomAgentID: item.ClassroomAgentID, SenderSnapshot: item.SenderSnapshot, Content: item.Content, Status: item.Status, ReplyToMessageID: item.ReplyToMessageID, TokenCount: item.TokenCount, Metadata: item.Metadata, CreatedAt: item.CreatedAt}
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
