package worker

import (
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

// Queue 把生成任务投进 asynq，满足课堂服务的 JobQueue。
type Queue struct {
	client   *asynq.Client
	maxRetry int
	timeout  time.Duration
}

// NewQueue 构造任务投递器。
func NewQueue(client *asynq.Client, maxRetry int, timeout time.Duration) *Queue {
	return &Queue{client: client, maxRetry: maxRetry, timeout: timeout}
}

// Enqueue 投递一个课堂生成任务，任务已存在时视为投递成功。
func (q *Queue) Enqueue(classroomID uint64) error {
	task, err := NewClassroomGenerateTask(classroomID, q.maxRetry, q.timeout)
	if err != nil {
		return err
	}
	// 任务 ID 就是课程 ID，重复投递本就该去重；已存在说明已有任务在跑，不是失败。
	if _, err := q.client.Enqueue(task); err != nil && !errors.Is(err, asynq.ErrTaskIDConflict) {
		return fmt.Errorf("投递生成任务失败: %w", err)
	}
	return nil
}
