package classroom

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"narra/internal/model/entity"
	"narra/pkg/logger"
	"narra/pkg/utils"
)

// Generate 跑一趟完整生成并返回错误，置失败由调用方在重试耗尽时决定。
func Generate(ctx context.Context, deps Deps, classroomID uint64) (err error) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("生成任务 panic", zap.Uint64("classroom_id", classroomID), zap.Any("panic", r))
			err = fmt.Errorf("生成过程异常: %v", r)
		}
	}()

	// 这一趟生成要能被外部停掉：删课堂时调用方会通过取消登记表按课程 ID 喊停。
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	unregister := registerCancel(classroomID, cancel)
	defer unregister()

	// 整课预算：页数与单页调用数各自的闸门都拦不住「页数 × 单页时长」，
	// 总时长只有这一层管得住。到点的收尾方式见 generateScenes。
	if deps.MaxDuration > 0 {
		var budgetCancel context.CancelFunc
		runCtx, budgetCancel = context.WithTimeoutCause(runCtx, deps.MaxDuration, errBudgetExceeded)
		defer budgetCancel()
	}

	classroom, err := deps.Classrooms.FindByID(runCtx, classroomID)
	if err != nil {
		return fmt.Errorf("读取课程失败: %w", err)
	}

	if classroom.Status == entity.ClassroomStatusGenerating {
		runID, runErr := EnsureRunID(runCtx, deps, classroom)
		if runErr != nil {
			return runErr
		}
		plan, planErr := planClassroom(runCtx, deps, classroom, runID)
		if planErr != nil {
			return planErr
		}
		logger.Info("课堂计划生成完成",
			zap.Uint64("classroom_id", classroomID),
			zap.String("run_id", runID),
			zap.Int("pages", len(plan.Pages)),
		)
	}

	var config GenerationConfig
	if err := json.Unmarshal(classroom.GenerationConfig, &config); err != nil {
		return fmt.Errorf("解析生成配置失败: %w", err)
	}
	return generateScenes(runCtx, deps, classroom, config)
}

// GenerateScene 只生成指定页面，供用户手动重试失败页。
func GenerateScene(ctx context.Context, deps Deps, classroomID, sceneID uint64) error {
	classroom, err := deps.Classrooms.FindByID(ctx, classroomID)
	if err != nil {
		return fmt.Errorf("读取课程失败: %w", err)
	}
	scenes, err := deps.Scenes.ListByClassroom(ctx, classroomID)
	if err != nil {
		return fmt.Errorf("读取课堂页面失败: %w", err)
	}
	var target *entity.Scene
	for i := range scenes {
		if scenes[i].ID == sceneID {
			target = &scenes[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("页面不存在")
	}
	var config GenerationConfig
	if err := json.Unmarshal(classroom.GenerationConfig, &config); err != nil {
		return fmt.Errorf("解析生成配置失败: %w", err)
	}
	plan := classroomPlanOf(classroom, scenes)
	runID, err := EnsureRunID(ctx, deps, classroom)
	if err != nil {
		return err
	}
	owner, err := newLeaseOwner(runID)
	if err != nil {
		return err
	}
	teacher, voice, err := classroomTeacher(ctx, deps, classroomID)
	if err != nil {
		return err
	}
	rt, err := newRuntime(ctx, deps, config.ProviderID, config.ModelID, config.WebSearch)
	if err != nil {
		return err
	}
	executor := &pageExecutor{
		deps: deps, classroom: classroom, teacher: teacher, voice: voice, rt: rt,
		context: buildClassroomContext(classroom, plan), outline: outlineIndex(plan.Pages),
		ttsPool: newTTSLimiter(effectiveTTSPoolSize(deps)), materialIDs: materialDocumentIDs(config),
		runID: runID, owner: owner,
	}
	return executor.run(ctx, pageTask{Page: planPageForScene(plan, *target), Scene: *target})
}

func planPageForScene(plan *ClassroomPlan, scene entity.Scene) PlanPage {
	for _, page := range plan.Pages {
		if page.SceneID == scene.ID || page.Order == int(scene.SortOrder) {
			return page
		}
	}
	return PlanPage{SceneID: scene.ID, Order: int(scene.SortOrder), Type: scene.Type, Title: scene.Title, Brief: scene.Brief}
}

// EnsureRunID 取本轮生成的运行标识：已落库就沿用，没有就生成一个写回去。
//
// 受理时就会写入，这里只兜住改造之前建下的旧课堂；链路追踪也用它当 trace 标识。
func EnsureRunID(ctx context.Context, deps Deps, classroom *entity.Classroom) (string, error) {
	if runID := strings.TrimSpace(classroom.GenerationRunID); runID != "" {
		return runID, nil
	}
	runID, err := utils.RandomHex(16)
	if err != nil {
		return "", fmt.Errorf("生成运行标识失败: %w", err)
	}
	if err := deps.Classrooms.UpdateRunID(ctx, classroom.ID, runID); err != nil {
		return "", fmt.Errorf("写入运行标识失败: %w", err)
	}
	classroom.GenerationRunID = runID
	return runID, nil
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
