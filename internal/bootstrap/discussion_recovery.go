package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"narra/internal/model/entity"
	"narra/internal/repository"
)

const discussionRestartError = "服务重启，讨论已中断，请重新发送消息"

// ReconcileDiscussions 将上次进程留下的未完成讨论收尾，保留已写入的消息。
// 本地部署只有一个服务进程；在对外提供接口前调用，避免把当前进程的运行误判为中断。
func ReconcileDiscussions(
	ctx context.Context,
	tx repository.TransactionManager,
	runs repository.RunRepository,
	turns repository.TurnRepository,
	messages repository.MessageRepository,
	events repository.ConversationEventRepository,
) (int, error) {
	unfinished, err := runs.ListUnfinished(ctx)
	if err != nil {
		return 0, fmt.Errorf("读取未完成讨论失败: %w", err)
	}
	payload, err := json.Marshal(struct {
		Error string `json:"error"`
	}{Error: discussionRestartError})
	if err != nil {
		return 0, err
	}
	recovered := 0
	for _, run := range unfinished {
		if err := tx.Run(ctx, func(ctx context.Context) error {
			finishedAt := time.Now().UTC()
			changed, err := runs.FailUnfinished(ctx, run.ID, discussionRestartError, finishedAt)
			if err != nil || !changed {
				return err
			}
			if err := turns.FailUnfinishedByRun(ctx, run.ID, discussionRestartError, finishedAt); err != nil {
				return err
			}
			if err := messages.FailStreamingByRun(ctx, run.ID); err != nil {
				return err
			}
			return events.AppendNext(ctx, &entity.ConversationEvent{
				ConversationID: run.ConversationID,
				RunID:          &run.ID,
				EventType:      entity.ConversationEventRunFailed,
				Payload:        payload,
			})
		}); err != nil {
			return recovered, fmt.Errorf("收尾讨论运行 #%d 失败: %w", run.ID, err)
		}
		recovered++
	}
	return recovered, nil
}
