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
const TypeSceneGenerate = "classroom:scene:generate"

// ClassroomGeneratePayload 是「生成课堂」任务的载荷，只带课程 ID，数据留在数据库。
type ClassroomGeneratePayload struct {
	ClassroomID uint64 `json:"classroom_id"`
}

type SceneGeneratePayload struct {
	ClassroomID uint64 `json:"classroom_id"`
	SceneID     uint64 `json:"scene_id"`
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

func NewSceneGenerateTask(classroomID, sceneID uint64, maxRetry int, timeout time.Duration) (*asynq.Task, error) {
	payload, err := json.Marshal(SceneGeneratePayload{ClassroomID: classroomID, SceneID: sceneID})
	if err != nil {
		return nil, fmt.Errorf("序列化页面重试载荷失败: %w", err)
	}
	return asynq.NewTask(TypeSceneGenerate, payload,
		asynq.TaskID(fmt.Sprintf("scene-%d-%d", sceneID, time.Now().UnixNano())),
		asynq.MaxRetry(maxRetry),
		asynq.Timeout(timeout),
	), nil
}

func DecodeSceneGenerate(payload []byte) (uint64, uint64, error) {
	var p SceneGeneratePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return 0, 0, fmt.Errorf("解析页面重试载荷失败: %w", err)
	}
	return p.ClassroomID, p.SceneID, nil
}
