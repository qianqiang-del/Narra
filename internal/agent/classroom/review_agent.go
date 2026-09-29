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

// toolNameSubmitReview 是审核交卷用的工具名。
const toolNameSubmitReview = "submit_page_review"

// reviewInput 是审核专家的输入：本页计划、最终内容、最终讲稿与交互页的 HTML 文档。
type reviewInput struct {
	Page      PageContext
	Plan      *PageExecutionPlan
	Blocks    []contentBlock
	Narration []narrationSegment
	HTML      string
}

// maxReviewHTMLBytes 是退回原文时送进审核的交互 HTML 上限。
//
// 正常路径走 summarizeHTMLForReview，抽可见文本与交互清单，不走这里；
// 只有 HTML 解析失败、退回"截断原文"时才用得上，兜住一次调用的体积。
const maxReviewHTMLBytes = 48 * 1024

// reviewPage 审核一页内容与讲稿的质量：一次调用加强制结构化输出，不挂工具。
//
// 模型调用本身出错（超时、限流、响应读断）也重试一次：审核是一次长调用，一次抖动不该让
// 这一页丢掉审核结论——审核失败不否决页面，但页面记录里就只剩一句"审核未完成"。
func reviewPage(ctx context.Context, rt *runtime, in *reviewInput) (*ReviewResult, error) {
	system, ok := agent.BuildTaskPrompt(agent.TaskReview)
	if !ok {
		return nil, fmt.Errorf("审核提示词未注册")
	}
	messages := reviewMessages(system, in)

	var lastErr error
	for attempt := 0; attempt <= maxValidateRetry; attempt++ {
		arguments, err := invokeWithRetryIf(ctx, maxTransientRetry, retryableModelError, func() (string, error) {
			return rt.generateToolCall(ctx, messages, reviewToolInfo())
		})
		if err != nil {
			return nil, fmt.Errorf("审核这一页失败: %w", err)
		}
		review, parseErr := parseReview(arguments)
		if parseErr == nil {
			return review, nil
		}
		lastErr = parseErr
		messages = append(messages, schema.UserMessage(fmt.Sprintf(
			"上一次提交的审核结果不合规：%s\n请修正后重新调用 %s 提交完整结果，不要解释。",
			parseErr.Error(), toolNameSubmitReview)))
	}
	return nil, fmt.Errorf("审核结果连续 %d 次校验不通过: %w", maxValidateRetry+1, lastErr)
}

// parseReview 解析并校验审核结论的参数。
func parseReview(arguments string) (*ReviewResult, error) {
	var review ReviewResult
	if err := json.Unmarshal([]byte(arguments), &review); err != nil {
		return nil, fmt.Errorf("解析审核结论失败: %w", err)
	}
	normalizeReview(&review)
	return &review, nil
}

// reviewPrompt 拼审核专家的用户提示词。
func reviewPrompt(in *reviewInput) string {
	var builder strings.Builder
	builder.WriteString(in.Page.focusedPagePrompt())
	if in.Plan != nil && len(in.Plan.AcceptanceCriteria) > 0 {
		builder.WriteString("\n## 本页验收条件\n")
		for _, item := range in.Plan.AcceptanceCriteria {
			fmt.Fprintf(&builder, "- %s\n", item)
		}
	}
	fmt.Fprintf(&builder, "\n## 最终内容块\n%s\n", renderBlocksForReview(in.Blocks))
	if document := strings.TrimSpace(in.HTML); document != "" {
		fmt.Fprintf(&builder, "\n## 最终交互页面\n%s\n", summarizeHTMLForReview(document))
	}
	fmt.Fprintf(&builder, "\n## 最终讲稿\n%s\n", renderNarrationForReview(in.Narration))
	return builder.String()
}

func reviewMessages(system string, in *reviewInput) []*schema.Message {
	return []*schema.Message{
		schema.SystemMessage(system),
		schema.UserMessage(in.Page.stableClassroomPrompt()),
		schema.UserMessage(reviewPrompt(in)),
	}
}

// reviewToolInfo 返回审核结论的交卷工具声明。
func reviewToolInfo() *schema.ToolInfo {
	return &schema.ToolInfo{
		Name: toolNameSubmitReview,
		Desc: "提交本页的审核结论。逐条比对验收条件后调用本工具交卷，每一页只调用一次。",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(objectSchema("一页的审核结论",
			[]string{"approved", "score", "issues"},
			schemaField{"approved", valueSchema("boolean", "这一页是否达到可用标准")},
			schemaField{"score", valueSchema("integer", "0-100 的质量分")},
			schemaField{"issues", arraySchema(reviewIssueSchema(), "发现的问题，按需要修订的顺序排列")},
			schemaField{"revision_instruction", valueSchema("string", "给专家的修订指令，通过时留空")},
		)),
	}
}

// reviewIssueSchema 返回一个审核问题的参数 schema。
func reviewIssueSchema() *jsonschema.Schema {
	return objectSchema("审核发现的一个问题", []string{"target", "message", "severity"},
		schemaField{"target", valueSchema("string", "plan / research / content / narration / both，指这个问题该由谁改")},
		schemaField{"code", valueSchema("string", "问题类型的短标识，如 MISSING_EXPLANATION")},
		schemaField{"message", valueSchema("string", "问题是什么、缺什么，要具体到能直接照着改")},
		schemaField{"severity", valueSchema("string", "minor / major / blocker")},
	)
}
