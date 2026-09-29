package service

import (
	"context"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
)

// ConversationService 是课堂对话的 HTTP 面。
//
// 对话资源的创建、列表、历史消息查询与事件流都通过这个接口暴露；
// 事件的写入仍由讨论编排器负责，服务层只负责读取和 DTO 映射。
type ConversationService interface {
	List(ctx context.Context, classroomID uint64) ([]responsedto.Conversation, error)
	Create(ctx context.Context, classroomID uint64, input requestdto.CreateConversation) (*responsedto.Conversation, error)
	ListMessages(ctx context.Context, conversationID uint64, afterSequence int64, limit int) ([]responsedto.ConversationMessage, error)

	// Exists 判断对话是否存在。SSE 端点在开流之前用它决定：回统一信封的
	// "对话不存在"，还是把响应切成事件流。
	Exists(ctx context.Context, id uint64) (bool, error)

	// ListEventsAfter 取序号大于 after 的事件（升序，最多 limit 条）。
	// after 是客户端最后收到的序号，0 表示从头重放。
	ListEventsAfter(ctx context.Context, conversationID uint64, after int64, limit int) ([]responsedto.ConversationEvent, error)
}
