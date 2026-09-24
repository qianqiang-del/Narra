package classroom

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"

	"narra/internal/agent"
	"narra/internal/model/entity"
)

// completeSceneTitle 是代码追加的结束页标题，前端的完成页组件不用这个值渲染。
const completeSceneTitle = "课程完成"

// GenerationConfig 是 classrooms.generation_config 里本次生成会用到的部分。
type GenerationConfig struct {
	ProviderID uint64 `json:"llm_provider_id"`
	ModelID    string `json:"llm_model_id"`
	WebSearch  bool   `json:"web_search"`
	Bio        string `json:"bio"`
}

// generateOutline 段一：生成大纲、建场景行，成功时把课程置为 playable。
// 返回的计划里带着落库后的场景 ID，供段二使用。
func generateOutline(ctx context.Context, deps Deps, classroom *entity.Classroom) (*outlinePlan, error) {
	var config GenerationConfig
	if err := json.Unmarshal(classroom.GenerationConfig, &config); err != nil {
		return nil, fmt.Errorf("解析 generation_config 失败: %w", err)
	}

	// 联网只由用户的开关决定，关了就不搜。
	allowSearch := config.WebSearch

	run, err := newRuntime(ctx, deps, config.ProviderID, config.ModelID, allowSearch)
	if err != nil {
		return nil, err
	}
	outline, err := run.outlineAgent(ctx)
	if err != nil {
		return nil, err
	}

	messages, err := buildOutlineMessages(classroom, config)
	if err != nil {
		return nil, err
	}
	message, err := outline.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("大纲生成失败: %w", err)
	}
	plan, err := parseOutline(message.Content)
	if err != nil {
		return nil, err
	}

	// 大纲标题回填课程：受理时只塞了截断需求的兜底值。
	if err := deps.Classrooms.UpdateTitle(ctx, classroom.ID, plan.Title); err != nil {
		return nil, fmt.Errorf("更新课程标题失败: %w", err)
	}
	if err := saveScenes(ctx, deps, classroom.ID, plan); err != nil {
		return nil, err
	}
	if err := deps.Classrooms.UpdateStatus(ctx, classroom.ID, entity.ClassroomStatusPlayable, nil); err != nil {
		return nil, fmt.Errorf("更新课程状态失败: %w", err)
	}
	return plan, nil
}

// buildOutlineMessages 拼大纲的输入。纯函数，便于单测。
func buildOutlineMessages(classroom *entity.Classroom, config GenerationConfig) ([]*schema.Message, error) {
	systemPrompt, ok := agent.BuildTaskPrompt(agent.TaskOutline)
	if !ok {
		return nil, fmt.Errorf("大纲提示词未注册")
	}

	var input strings.Builder
	input.WriteString("## 用户需求\n")
	input.WriteString(classroom.Requirement)
	input.WriteString("\n\n## 深度交互\n")
	if classroom.Mode == entity.ClassroomModeInteractive {
		input.WriteString("开启")
	} else {
		input.WriteString("未开启")
	}
	if bio := strings.TrimSpace(config.Bio); bio != "" {
		input.WriteString("\n\n## 用户简介\n")
		input.WriteString(bio)
	}

	return []*schema.Message{
		schema.SystemMessage(systemPrompt),
		schema.UserMessage(input.String()),
	}, nil
}

// saveScenes 建场景行，并把回填的 ID 写回 plan。
//
// 结束页由代码追加、直接置 ready：它没有内容块和讲稿，前端用单独的完成页组件渲染；
// 留成 pending 会让收尾时「全部 ready」永远不成立。
func saveScenes(ctx context.Context, deps Deps, classroomID uint64, plan *outlinePlan) error {
	// 任务可能重复执行（重试、崩溃重投），先清掉旧场景，免得撞唯一约束。
	if err := deps.Scenes.DeleteByClassroom(ctx, classroomID); err != nil {
		return fmt.Errorf("清理已有场景失败: %w", err)
	}

	scenes := make([]*entity.Scene, 0, len(plan.Scenes)+1)
	for index := range plan.Scenes {
		scenes = append(scenes, &entity.Scene{
			ClassroomID: classroomID,
			SortOrder:   int32(index),
			Type:        plan.Scenes[index].Type,
			Title:       plan.Scenes[index].Title,
			Brief:       plan.Scenes[index].Brief,
			Status:      entity.SceneStatusPending,
			Content:     json.RawMessage("{}"),
		})
	}
	scenes = append(scenes, &entity.Scene{
		ClassroomID: classroomID,
		SortOrder:   int32(len(plan.Scenes)),
		Type:        entity.SceneTypeComplete,
		Title:       completeSceneTitle,
		Status:      entity.SceneStatusReady,
		Content:     json.RawMessage("{}"),
	})

	if err := deps.Scenes.CreateBatch(ctx, scenes); err != nil {
		return fmt.Errorf("保存场景失败: %w", err)
	}
	for index := range plan.Scenes {
		plan.Scenes[index].SceneID = scenes[index].ID
	}
	return nil
}
