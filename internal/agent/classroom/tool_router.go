package classroom

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"

	"narra/internal/agent"
)

// toolNameEmitPagePlan 是页执行计划交卷用的工具名。
const toolNameEmitPagePlan = "emit_page_execution_plan"

// PageExecutionPlan 是单页的小型执行计划，决定这一页要不要查资料、按什么标准验收。
type PageExecutionPlan struct {
	RequiresTools       bool       `json:"requires_tools"`
	ToolSteps           []ToolStep `json:"tool_steps"`
	ContentRequirements []string   `json:"content_requirements"`
	AcceptanceCriteria  []string   `json:"acceptance_criteria"`
}

// ToolStep 是这一页打算执行的一次工具调用。
type ToolStep struct {
	Tool    string `json:"tool"`
	Query   string `json:"query"`
	Purpose string `json:"purpose"`
}

// planPage 让页规划专家交出一页的执行计划；一次调用加强制结构化输出，不挂工具。
func planPage(ctx context.Context, rt *runtime, page PageContext) (*PageExecutionPlan, error) {
	system, ok := agent.BuildTaskPrompt(agent.TaskPagePlan)
	if !ok {
		return nil, fmt.Errorf("页规划提示词未注册")
	}
	messages := []*schema.Message{schema.SystemMessage(system), schema.UserMessage(pagePlanPrompt(page, rt.toolNames(ctx)))}

	var lastErr error
	for attempt := 0; attempt <= maxValidateRetry; attempt++ {
		arguments, err := rt.generateToolCall(ctx, messages, pagePlanToolInfo())
		if err != nil {
			return nil, fmt.Errorf("规划这一页失败: %w", err)
		}
		plan, parseErr := parsePagePlan(arguments)
		if parseErr == nil {
			return plan, nil
		}
		lastErr = parseErr
		messages = append(messages, schema.UserMessage(fmt.Sprintf(
			"上一次提交的计划不合规：%s\n请修正后重新调用 %s 提交完整计划，不要解释。",
			parseErr.Error(), toolNameEmitPagePlan)))
	}
	return nil, fmt.Errorf("页执行计划连续 %d 次校验不通过: %w", maxValidateRetry+1, lastErr)
}

// parsePagePlan 解析并校验页执行计划的参数。
func parsePagePlan(arguments string) (*PageExecutionPlan, error) {
	var plan PageExecutionPlan
	if err := json.Unmarshal([]byte(arguments), &plan); err != nil {
		return nil, fmt.Errorf("解析页执行计划失败: %w", err)
	}
	plan.ContentRequirements = normalizeTexts(plan.ContentRequirements)
	plan.AcceptanceCriteria = normalizeTexts(plan.AcceptanceCriteria)
	if len(plan.AcceptanceCriteria) == 0 {
		plan.AcceptanceCriteria = []string{"内容覆盖本页要讲清的要点", "内容与讲稿一一对应"}
	}
	steps := make([]ToolStep, 0, len(plan.ToolSteps))
	for _, step := range plan.ToolSteps {
		step.Tool = strings.TrimSpace(step.Tool)
		step.Query = strings.TrimSpace(step.Query)
		step.Purpose = strings.TrimSpace(step.Purpose)
		if step.Tool == "" || step.Query == "" {
			continue
		}
		steps = append(steps, step)
	}
	plan.ToolSteps = steps
	return &plan, nil
}

// pagePlanPrompt 拼页规划专家的用户提示词。
func pagePlanPrompt(page PageContext, tools []string) string {
	var builder strings.Builder
	page.writePrompt(&builder)
	builder.WriteString("\n## 本页可用的工具\n")
	if len(tools) == 0 {
		builder.WriteString("本次没有可用工具，requires_tools 只能是 false。\n")
	} else {
		fmt.Fprintf(&builder, "%s\n", strings.Join(tools, "、"))
	}
	if len(page.Current.SuggestedTools) > 0 {
		fmt.Fprintf(&builder, "规划阶段对这类页面的建议：%s（只是建议，最终由你判断）\n",
			strings.Join(page.Current.SuggestedTools, "、"))
	}
	builder.WriteString("\n内容完全可以依据课堂上下文写出来时，requires_tools 填 false、tool_steps 填空数组。\n")
	return builder.String()
}

// pagePlanToolInfo 返回页执行计划的交卷工具声明。
func pagePlanToolInfo() *schema.ToolInfo {
	return &schema.ToolInfo{
		Name: toolNameEmitPagePlan,
		Desc: "提交本页的执行计划。思考完成后调用本工具交卷，每一页只调用一次。",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(objectSchema("单页执行计划",
			[]string{"requires_tools", "content_requirements", "acceptance_criteria"},
			schemaField{"requires_tools", valueSchema("boolean", "这一页是否需要查资料或调工具")},
			schemaField{"tool_steps", arraySchema(toolStepSchema(), "打算执行的工具调用，最多 3 步")},
			schemaField{"content_requirements", arraySchema(valueSchema("string", "这一页必须包含的内容"), "必须包含的内容要点")},
			schemaField{"acceptance_criteria", arraySchema(valueSchema("string", "可逐条比对的验收条件"), "这一页的验收条件")},
		)),
	}
}

// toolStepSchema 返回一次工具调用的参数 schema。
func toolStepSchema() *jsonschema.Schema {
	return objectSchema("一次工具调用", []string{"tool", "query"},
		schemaField{"tool", valueSchema("string", "工具名，必须来自给出的可用工具")},
		schemaField{"query", valueSchema("string", "查询词或检索词")},
		schemaField{"purpose", valueSchema("string", "这次调用要支撑本页哪个事实")},
	)
}
