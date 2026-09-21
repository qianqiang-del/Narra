package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/hibiken/asynq"
)

// defaultQueue 是任务默认所属的队列。
const defaultQueue = "default"

// Server 是 asynq 消费端，取任务并交给注册的处理器。
type Server struct {
	server    *asynq.Server
	mux       *asynq.ServeMux
	inspector *asynq.Inspector
}

// NewServer 构造消费端。concurrency 是同时处理的任务数。
func NewServer(redisOpt asynq.RedisClientOpt, concurrency int) *Server {
	return &Server{
		server:    asynq.NewServer(redisOpt, asynq.Config{Concurrency: concurrency, LogLevel: asynq.WarnLevel}),
		mux:       asynq.NewServeMux(),
		inspector: asynq.NewInspector(redisOpt),
	}
}

// Register 注册某类任务的处理器，fn 返回的错误决定 asynq 是否重试。
func (s *Server) Register(taskType string, fn func(context.Context, []byte) error) {
	s.mux.HandleFunc(taskType, func(ctx context.Context, task *asynq.Task) error {
		return fn(ctx, task.Payload())
	})
}

// Start 启动消费端，不阻塞。
func (s *Server) Start() error {
	if err := s.server.Start(s.mux); err != nil {
		return fmt.Errorf("启动生成任务消费端失败: %w", err)
	}
	return nil
}

// Shutdown 优雅关闭，等在途任务跑完；超时未完成的会被推回队列。
func (s *Server) Shutdown() {
	s.server.Shutdown()
	_ = s.inspector.Close()
}

// archivedScanSize 是一次归档名单最多取多少条。
const archivedScanSize = 1000

// ArchivedTaskIDs 返回归档队列里的任务 ID 集合，归档意味着重试已耗尽、不会再自动处理。
func (s *Server) ArchivedTaskIDs() (map[string]struct{}, error) {
	tasks, err := s.inspector.ListArchivedTasks(defaultQueue, asynq.PageSize(archivedScanSize))
	if err != nil {
		return nil, fmt.Errorf("读取归档任务失败: %w", err)
	}
	ids := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		ids[task.ID] = struct{}{}
	}
	return ids, nil
}

// TaskExists 报告某任务在队列里是否还存在（含归档）。
func (s *Server) TaskExists(taskID string) (bool, error) {
	if _, err := s.inspector.GetTaskInfo(defaultQueue, taskID); err != nil {
		if errors.Is(err, asynq.ErrTaskNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("查询任务状态失败: %w", err)
	}
	return true, nil
}

// Permanent 把错误标记为「重试也不会成功」。
func Permanent(err error) error {
	return fmt.Errorf("%w: %w", err, asynq.SkipRetry)
}

// Retried 返回当前任务已重试的次数，取不到时为 0。
func Retried(ctx context.Context) int {
	n, ok := asynq.GetRetryCount(ctx)
	if !ok {
		return 0
	}
	return n
}

// Exhausted 报告当前是否已是最后一次尝试。
func Exhausted(ctx context.Context) bool {
	retried, ok := asynq.GetRetryCount(ctx)
	if !ok {
		return false
	}
	max, ok := asynq.GetMaxRetry(ctx)
	if !ok {
		return false
	}
	return retried >= max
}
