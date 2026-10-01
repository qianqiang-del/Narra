package classroom

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"narra/internal/agent"
)

// narrationInput 是讲稿专家的输入：本页分层上下文、页执行计划、已校验的内容块与修订反馈。
type narrationInput struct {
	Page PageContext
	// Plan 只取其中的验收条件：讲稿要照着已定稿的内容块讲，不必再给「必须包含」，
	// 否则容易把内容该讲的东西塞进讲稿，反而制造讲稿脱离页面的问题。
	Plan     *PageExecutionPlan
	Blocks   []contentBlock
	Revision string
	Feedback string
}

// buildNarrationChain 建讲稿专家的执行单元：拼提示词 → 模型 → 解析成讲稿段落。
func buildNarrationChain(ctx context.Context, rt *runtime) (compose.Runnable[*narrationInput, []narrationSegment], error) {
	chain := compose.NewChain[*narrationInput, []narrationSegment]()
	chain.AppendLambda(compose.InvokableLambda(func(_ context.Context, in *narrationInput) ([]*schema.Message, error) {
		system, ok := agent.BuildSystemPrompt(in.Page.Teacher, agent.TaskNarration)
		if !ok {
			return nil, fmt.Errorf("教师讲稿提示词未注册")
		}
		return narrationMessages(system, in), nil
	})).AppendChatModel(rt.chatModel).AppendLambda(compose.InvokableLambda(func(_ context.Context, msg *schema.Message) ([]narrationSegment, error) {
		var items []narrationSegment
		if err := unmarshalArrayLoose(msg.Content, &items); err != nil {
			return nil, malformedOutput(err)
		}
		return items, nil
	}))
	return chain.Compile(ctx)
}

// generateNarration 生成一页讲稿并校验，不合规就带着提示重跑同一条 Chain。
func generateNarration(ctx context.Context, rt *runtime, budget *pageBudget, in *narrationInput) ([]narrationSegment, error) {
	chain, err := buildNarrationChain(ctx, rt)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt <= maxValidateRetry; attempt++ {
		if !budget.trySpend() {
			lastErr = fmt.Errorf("这一页的模型调用预算已用尽")
			break
		}
		items, runErr := chain.Invoke(ctx, in)
		if runErr != nil {
			lastErr = runErr
			if !retryableModelError(runErr) && !isMalformedOutput(runErr) {
				break
			}
			in.Feedback = outputFeedback(runErr)
			continue
		}
		validated, validateErr := validateNarration(items, in.Blocks)
		if validateErr == nil {
			return validated, nil
		}
		lastErr = validateErr
		in.Feedback = outputFeedback(validateErr)
	}
	return nil, lastErr
}

// narrationBlockView 是讲稿专家看到的内容块：key、type、正文，测验块再带上题干选项。
// 交互块的 config（深交互的整份配置）讲稿用不上，留在提示词里只会白占篇幅。
type narrationBlockView struct {
	Key     string   `json:"key"`
	Type    string   `json:"type"`
	Content string   `json:"content"`
	Options []string `json:"options,omitempty"`
}

// narrationBlocks 把已定稿的内容块压成讲稿视图。
func narrationBlocks(blocks []contentBlock) []narrationBlockView {
	views := make([]narrationBlockView, 0, len(blocks))
	for _, block := range blocks {
		view := narrationBlockView{Key: block.Key, Type: block.Type, Content: block.Content}
		if block.Interaction != nil {
			view.Options = block.Interaction.Options
		}
		views = append(views, view)
	}
	return views
}

// narrationUserPrompt 拼讲稿专家的用户提示词；讲稿照着已定稿的内容讲，不带证据包。
func narrationUserPrompt(in *narrationInput) string {
	var builder strings.Builder
	builder.WriteString(in.Page.executionPagePrompt())
	writeAcceptanceCriteria(&builder, in.Plan)
	raw, _ := json.Marshal(narrationBlocks(in.Blocks))
	fmt.Fprintf(&builder, "\n## 已校验内容块\n%s\n每个块一段讲解，每段不超过 120 字。\n", raw)
	writeRevision(&builder, in.Revision, in.Feedback)
	return builder.String()
}

func narrationMessages(system string, in *narrationInput) []*schema.Message {
	return []*schema.Message{
		schema.SystemMessage(system),
		schema.UserMessage(in.Page.stableClassroomPrompt()),
		schema.UserMessage(narrationUserPrompt(in)),
	}
}
