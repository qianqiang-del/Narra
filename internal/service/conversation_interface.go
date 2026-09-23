package service

import (
	"context"

	responsedto "narra/internal/model/dto/response"
)

// ConversationService 是课堂对话的 HTTP 面。
//
// 目前只有事件流一件事：对话本身的创建、列表、关闭属于编排与工作台链路，
// 等那条链路落地时再往这个接口上加方法 —— 与 ConversationRepository 同一种做法，
// 接口按业务动作开，不留"定义了没人用"的空方法。
type ConversationService interface {
	// Exists 判断对话是否存在。SSE 端点在开流之前用它决定：回统一信封的
	// "对话不存在"，还是把响应切成事件流。
	Exists(ctx context.Context, id uint64) (bool, error)

	// ListEventsAfter 取序号大于 after 的事件（升序，最多 limit 条）。
	// after 是客户端最后收到的序号，0 表示从头重放。
	ListEventsAfter(ctx context.Context, conversationID uint64, after int64, limit int) ([]responsedto.ConversationEvent, error)
}
