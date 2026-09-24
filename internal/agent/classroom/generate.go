package classroom

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"narra/internal/model/entity"
	"narra/pkg/logger"
)

// Generate 跑一趟完整生成并返回错误，置失败由调用方在重试耗尽时决定。
func Generate(ctx context.Context, deps Deps, classroomID uint64) (err error) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("生成任务 panic", zap.Uint64("classroom_id", classroomID), zap.Any("panic", r))
			err = fmt.Errorf("生成过程异常: %v", r)
		}
	}()

	classroom, err := deps.Classrooms.FindByID(ctx, classroomID)
	if err != nil {
		return fmt.Errorf("读取课程失败: %w", err)
	}

	if classroom.Status == entity.ClassroomStatusGenerating {
		if _, err := generateOutline(ctx, deps, classroom); err != nil {
			return err
		}
		logger.Info("课堂大纲生成完成", zap.Uint64("classroom_id", classroomID))
	}
	var config GenerationConfig
	if err := json.Unmarshal(classroom.GenerationConfig, &config); err != nil {
		return fmt.Errorf("解析生成配置失败: %w", err)
	}
	return generateScenes(ctx, deps, classroom, config)
}

// MarkFailed 把课程置为 failed 并写入失败原因，供调用方在重试耗尽时调用。
func MarkFailed(ctx context.Context, deps Deps, classroomID uint64, reason string) {
	// 任务超时会取消 ctx，这次写库必须脱离它，否则失败原因落不了库。
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	msg := truncateRunes(reason, 500)
	if err := deps.Classrooms.UpdateStatus(writeCtx, classroomID, entity.ClassroomStatusFailed, &msg); err != nil {
		logger.Error("置 failed 失败", zap.Uint64("classroom_id", classroomID), zap.Error(err))
	}
}
