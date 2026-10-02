package classroom

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	orderedmap "github.com/wk8/go-ordered-map/v2"
	"go.uber.org/zap"

	"narra/internal/agent"
	"narra/internal/material"
	"narra/internal/model/entity"
	"narra/pkg/logger"
)

// toolNameEmitPlan 是规划交卷用的工具名。
const toolNameEmitPlan = "emit_classroom_plan"

// maxPlannerStep 是规划 Agent 的图执行步数上限，要容得下联网后的多轮调研加一次交卷。
const maxPlannerStep = 20

// initialPlanVersion 是首轮规划的计划版本号。
const initialPlanVersion = 1

// completeSceneTitle 是代码追加的结束页标题，前端的完成页组件不用这个值渲染。
const completeSceneTitle = "课程完成"

// GenerationConfig 是 classrooms.generation_config 里本次生成会用到的部分。
type GenerationConfig struct {
	ProviderID uint64 `json:"llm_provider_id"`
	ModelID    string `json:"llm_model_id"`
	WebSearch  bool   `json:"web_search"`
	Bio        string `json:"bio"`
	// Materials 是受理时冻结的本课材料引用；生成侧据此定向取材料文本。
	Materials []material.Ref `json:"materials"`
}

// planClassroom 段一：生成课堂计划、计划落库、建场景行，成功时把课程置为 playable。
func planClassroom(ctx context.Context, deps Deps, classroom *entity.Classroom, runID string) (*ClassroomPlan, error) {
	var config GenerationConfig
	if err := json.Unmarshal(classroom.GenerationConfig, &config); err != nil {
		return nil, fmt.Errorf("解析 generation_config 失败: %w", err)
	}

	rt, err := newRuntime(ctx, deps, config.ProviderID, config.ModelID, config.WebSearch)
	if err != nil {
		return nil, err
	}
	planner, err := rt.plannerAgent(ctx)
	if err != nil {
		return nil, err
	}
	// 材料是加法：取不到（文档被删、检索故障）就按没材料排课，绝不阻断规划。
	// 走 rt.retrievalContext 是为了让"需求相关节选"这步能复用课堂模型做查询扩写。
	var materialBlocks []material.Block
	if deps.Materials != nil && len(config.Materials) > 0 {
		materialBlocks = deps.Materials.Snapshot(rt.retrievalContext(ctx), config.Materials, classroom.Requirement)
		if len(materialBlocks) == 0 {
			logger.Warn("本课材料没有可注入的内容",
				zap.Uint64("classroom_id", classroom.ID),
				zap.Int("materials", len(config.Materials)))
		} else {
			logger.Info("本课材料已注入规划",
				zap.Uint64("classroom_id", classroom.ID),
				zap.Int("blocks", len(materialBlocks)))
		}
	}
	messages, err := buildPlanMessages(classroom, config, materialBlocks)
	if err != nil {
		return nil, err
	}

	plan, err := generatePlan(rt.retrievalContext(ctx), planner, messages)
	if err != nil {
		return nil, err
	}
	if err := persistPlan(ctx, deps, classroom, plan, runID); err != nil {
		return nil, err
	}
	return plan, nil
}

// generatePlan 让规划 Agent 交卷并校验，不合规就带着错误回灌重试。
//
// 模型调用本身出错（超时、限流）也重试一次：规划是一次长调用，一次抖动不该让整段规划重来。
func generatePlan(ctx context.Context, planner *react.Agent, messages []*schema.Message) (*ClassroomPlan, error) {
	var lastErr error
	for attempt := 0; attempt <= maxValidateRetry; attempt++ {
		message, err := invokeWithRetryIf(ctx, maxTransientRetry, retryableModelError, func() (*schema.Message, error) {
			return planner.Generate(ctx, messages)
		})
		if err != nil {
			return nil, fmt.Errorf("规划课堂失败: %w", err)
		}
		plan, validateErr := parsePlan(message.Content)
		if validateErr == nil {
			return plan, nil
		}
		lastErr = validateErr
		logger.Warn("课堂计划不合规，重试", zap.Int("attempt", attempt+1), zap.Error(validateErr))
		messages = append(messages, schema.UserMessage(fmt.Sprintf(
			"上一次提交的计划不合规：%s\n请修正后重新调用 %s 提交完整计划，不要解释。",
			validateErr.Error(), toolNameEmitPlan)))
	}
	return nil, fmt.Errorf("课堂计划连续 %d 次校验不通过: %w", maxValidateRetry+1, lastErr)
}

// buildPlanMessages 拼规划的输入。纯函数，便于单测。
func buildPlanMessages(classroom *entity.Classroom, config GenerationConfig, materialBlocks []material.Block) ([]*schema.Message, error) {
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
	if section := formatMaterialSection(materialBlocks); section != "" {
		input.WriteString("\n\n")
		input.WriteString(section)
	}

	return []*schema.Message{
		schema.SystemMessage(systemPrompt),
		schema.UserMessage(input.String()),
	}, nil
}

// formatMaterialSection 把材料块拼成规划输入里的一节；没有材料时返回空串，
// 调用方的输入与"没有材料"时代完全一致。
func formatMaterialSection(blocks []material.Block) string {
	if len(blocks) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("## 本课材料\n")
	builder.WriteString("以下是用户为这门课提供的材料，排课应以其为依据。\n")
	for _, block := range blocks {
		builder.WriteString("\n### ")
		builder.WriteString(block.Name)
		if block.Truncated {
			builder.WriteString("（节选）")
		}
		builder.WriteString("\n")
		builder.WriteString(block.Text)
		builder.WriteString("\n")
	}
	return strings.TrimSpace(builder.String())
}

// parsePlan 解析并校验规划 Agent 交回的计划。
func parsePlan(raw string) (*ClassroomPlan, error) {
	var plan ClassroomPlan
	if err := unmarshalLoose(raw, &plan); err != nil {
		return nil, fmt.Errorf("解析课堂计划 JSON 失败: %w", err)
	}
	plan.Version = initialPlanVersion
	if err := validatePlan(&plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

// validatePlan 校验计划结构并就地归一化文本字段。
func validatePlan(plan *ClassroomPlan) error {
	plan.Title = truncateRunes(strings.TrimSpace(plan.Title), maxClassroomTitleRunes)
	if plan.Title == "" {
		return fmt.Errorf("计划缺少课程标题")
	}
	plan.Audience.Level = strings.TrimSpace(plan.Audience.Level)
	plan.Audience.Background = strings.TrimSpace(plan.Audience.Background)
	if plan.LearningObjectives = normalizeTexts(plan.LearningObjectives); len(plan.LearningObjectives) == 0 {
		return fmt.Errorf("计划缺少学习目标")
	}
	if len(plan.Pages) == 0 {
		return fmt.Errorf("计划里一页都没有")
	}
	if len(plan.Pages) > maxPlanPages {
		return fmt.Errorf("计划有 %d 页，超过 %d 页上限", len(plan.Pages), maxPlanPages)
	}

	position := make(map[string]int, len(plan.Pages))
	for index := range plan.Pages {
		page := &plan.Pages[index]
		page.PlanID = truncateRunes(strings.TrimSpace(page.PlanID), maxPlanPageIDRunes)
		if page.PlanID == "" {
			return fmt.Errorf("第 %d 页缺少 plan_id", index+1)
		}
		if _, duplicated := position[page.PlanID]; duplicated {
			return fmt.Errorf("plan_id %q 重复了", page.PlanID)
		}
		if page.Order != index {
			return fmt.Errorf("第 %d 页的 order 是 %d，必须与页序一致且从 0 连续递增", index+1, page.Order)
		}
		position[page.PlanID] = index

		page.Type = strings.TrimSpace(page.Type)
		if _, ok := allowedSceneTypes[page.Type]; !ok {
			return fmt.Errorf("第 %d 页的类型 %q 不在允许范围内", index+1, page.Type)
		}
		page.Title = truncateRunes(strings.TrimSpace(page.Title), maxSceneTitleRunes)
		if page.Title == "" {
			return fmt.Errorf("第 %d 页缺标题", index+1)
		}
		page.Brief = strings.TrimSpace(page.Brief)
		if len([]rune(page.Brief)) < minPlanBriefRunes {
			return fmt.Errorf("第 %d 页的 brief 太笼统，至少要 %d 字说清这一页讲哪几点", index+1, minPlanBriefRunes)
		}
		page.LearningObjective = strings.TrimSpace(page.LearningObjective)
		if len([]rune(page.LearningObjective)) < minLearningObjectiveRunes {
			return fmt.Errorf("第 %d 页缺少够具体的学习目标", index+1)
		}
		page.Prerequisites = normalizeTexts(page.Prerequisites)
		page.SuggestedTools = normalizeTexts(page.SuggestedTools)
		page.NarrationFocus = normalizeTexts(page.NarrationFocus)
		if page.EstimatedSeconds < 0 {
			page.EstimatedSeconds = 0
		}
	}

	for index := range plan.Pages {
		for _, dependency := range plan.Pages[index].Prerequisites {
			at, ok := position[dependency]
			if !ok {
				return fmt.Errorf("第 %d 页依赖的 %q 不在计划里", index+1, dependency)
			}
			if at >= index {
				return fmt.Errorf("第 %d 页依赖的 %q 不是排在前面的页", index+1, dependency)
			}
		}
	}
	return nil
}

// normalizeTexts 去掉每项首尾空白并丢弃空项。
func normalizeTexts(items []string) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// persistPlan 在一个事务里回填标题、保存计划、重建场景行并置课程为 playable。
func persistPlan(ctx context.Context, deps Deps, classroom *entity.Classroom, plan *ClassroomPlan, runID string) error {
	raw, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("编码课堂计划失败: %w", err)
	}
	return deps.Tx.Run(ctx, func(txCtx context.Context) error {
		if err := deps.Classrooms.UpdateTitle(txCtx, classroom.ID, plan.Title); err != nil {
			return fmt.Errorf("更新课程标题失败: %w", err)
		}
		if err := deps.Classrooms.SavePlan(txCtx, classroom.ID, raw, int32(plan.Version), runID); err != nil {
			return fmt.Errorf("保存课堂计划失败: %w", err)
		}
		if err := saveScenes(txCtx, deps, classroom.ID, plan); err != nil {
			return err
		}
		return deps.Classrooms.UpdateStatus(txCtx, classroom.ID, entity.ClassroomStatusPlayable, nil)
	})
}

// saveScenes 按计划建场景行，并把落库后的 ID 回填进计划。
//
// 结束页由代码追加、直接置 ready：它没有内容块和讲稿，前端用单独的完成页组件渲染；
// 留成 pending 会让收尾时「全部 ready」永远不成立。
func saveScenes(ctx context.Context, deps Deps, classroomID uint64, plan *ClassroomPlan) error {
	// 任务可能重复执行（重试、崩溃重投），先清掉旧场景，免得撞唯一约束。
	if err := deps.Scenes.DeleteByClassroom(ctx, classroomID); err != nil {
		return fmt.Errorf("清理已有场景失败: %w", err)
	}

	scenes := make([]*entity.Scene, 0, len(plan.Pages)+1)
	for index := range plan.Pages {
		page := plan.Pages[index]
		scenes = append(scenes, &entity.Scene{
			ClassroomID: classroomID,
			SortOrder:   int32(page.Order),
			Type:        page.Type,
			Title:       page.Title,
			Brief:       page.Brief,
			Status:      entity.SceneStatusPending,
			Content:     json.RawMessage("{}"),
		})
	}
	scenes = append(scenes, &entity.Scene{
		ClassroomID: classroomID,
		SortOrder:   int32(len(plan.Pages)),
		Type:        entity.SceneTypeComplete,
		Title:       completeSceneTitle,
		Status:      entity.SceneStatusReady,
		Content:     json.RawMessage("{}"),
	})

	if err := deps.Scenes.CreateBatch(ctx, scenes); err != nil {
		return fmt.Errorf("保存场景失败: %w", err)
	}
	for index := range plan.Pages {
		plan.Pages[index].SceneID = scenes[index].ID
	}
	return nil
}

// emitPlanTool 是规划交卷用的工具：把参数原样回传，由调用方解析成 ClassroomPlan。
type emitPlanTool struct{ info *schema.ToolInfo }

// Info 返回工具的元信息供规划 Agent 使用。
func (t *emitPlanTool) Info(context.Context) (*schema.ToolInfo, error) { return t.info, nil }

// InvokableRun 校验参数是合法 JSON 后原样返回。
func (t *emitPlanTool) InvokableRun(_ context.Context, arguments string, _ ...tool.Option) (string, error) {
	if !json.Valid([]byte(arguments)) {
		return "", fmt.Errorf("课堂计划不是合法 JSON")
	}
	return arguments, nil
}

// newEmitPlanTool 建交卷工具。
func newEmitPlanTool() tool.BaseTool {
	return &emitPlanTool{info: &schema.ToolInfo{
		Name:        toolNameEmitPlan,
		Desc:        "提交最终的课堂计划。资料查完之后调用本工具交卷，每次规划只调用一次。",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(planSchema()),
	}}
}

// planSchema 返回课堂计划的参数 schema。
func planSchema() *jsonschema.Schema {
	return objectSchema("一次课堂生成的完整计划", []string{"title", "learning_objectives", "audience", "pages"},
		schemaField{"title", valueSchema("string", "课程标题，不超过 30 字的名词短语")},
		schemaField{"learning_objectives", arraySchema(valueSchema("string", "一条学习目标"), "学员学完这门课能做到的事，2-5 条")},
		schemaField{"audience", objectSchema("学员画像", []string{"level", "background"},
			schemaField{"level", valueSchema("string", "beginner / intermediate / advanced")},
			schemaField{"background", valueSchema("string", "学员已知什么、缺什么")},
		)},
		schemaField{"pages", arraySchema(pageSchema(), "页面列表，最多 15 页")},
	)
}

// pageSchema 返回计划里一页的参数 schema。
func pageSchema() *jsonschema.Schema {
	return objectSchema("计划里的一页", []string{"plan_id", "order", "type", "title", "brief", "learning_objective"},
		schemaField{"plan_id", valueSchema("string", "页标识符，形如 page-01，全计划唯一")},
		schemaField{"order", valueSchema("integer", "页序，从 0 开始连续递增")},
		schemaField{"type", valueSchema("string", "slide / quiz / interactive 三选一")},
		schemaField{"title", valueSchema("string", "页面标题")},
		schemaField{"brief", valueSchema("string", "这一页讲哪几点，1-2 句清单式描述")},
		schemaField{"learning_objective", valueSchema("string", "学完这一页能做到什么")},
		schemaField{"prerequisites", arraySchema(valueSchema("string", "先修页的 plan_id"), "先修页的 plan_id，只能是排在前面的页")},
		schemaField{"suggested_tools", arraySchema(valueSchema("string", "工具名"), "这一页建议使用的工具，可留空")},
		schemaField{"narration_focus", arraySchema(valueSchema("string", "讲解要点"), "讲稿要重点讲清的几点")},
		schemaField{"estimated_seconds", valueSchema("integer", "这一页预计用时，秒")},
	)
}

// schemaField 是参数 schema 里一个具名属性。
type schemaField struct {
	name   string
	schema *jsonschema.Schema
}

// objectSchema 拼一个对象类型的参数 schema。
func objectSchema(description string, required []string, fields ...schemaField) *jsonschema.Schema {
	properties := orderedmap.New[string, *jsonschema.Schema]()
	for _, field := range fields {
		properties.Set(field.name, field.schema)
	}
	return &jsonschema.Schema{
		Type:        "object",
		Description: description,
		Properties:  properties,
		Required:    required,
	}
}

// arraySchema 拼一个数组类型的参数 schema。
func arraySchema(items *jsonschema.Schema, description string) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "array", Description: description, Items: items}
}

// valueSchema 拼一个标量类型的参数 schema。
func valueSchema(kind, description string) *jsonschema.Schema {
	return &jsonschema.Schema{Type: kind, Description: description}
}
