package worker

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/hibiken/asynq"
)

// TypeClassroomGenerate 是「生成课堂」任务的类型名。
const TypeClassroomGenerate = "classroom:generate"

// ClassroomGeneratePayload 是「生成课堂」任务的载荷，只带课程 ID，数据留在数据库。
type ClassroomGeneratePayload struct {
	ClassroomID uint64 `json:"classroom_id"`
}

// ClassroomTaskID 返回某课程对应的任务 ID。
func ClassroomTaskID(classroomID uint64) string {
	return strconv.FormatUint(classroomID, 10)
}

// NewClassroomGenerateTask 构造生成课堂任务，用课程 ID 当任务 ID 以便对账与去重。
func NewClassroomGenerateTask(classroomID uint64, maxRetry int, timeout time.Duration) (*asynq.Task, error) {
	payload, err := json.Marshal(ClassroomGeneratePayload{ClassroomID: classroomID})
	if err != nil {
		return nil, fmt.Errorf("序列化任务载荷失败: %w", err)
	}
	return asynq.NewTask(TypeClassroomGenerate, payload,
		asynq.TaskID(ClassroomTaskID(classroomID)),
		asynq.MaxRetry(maxRetry),
		asynq.Timeout(timeout),
	), nil
}

// DecodeClassroomGenerate 解析生成课堂任务的载荷。
func DecodeClassroomGenerate(payload []byte) (uint64, error) {
	var p ClassroomGeneratePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return 0, fmt.Errorf("解析任务载荷失败: %w", err)
	}
	return p.ClassroomID, nil
}
