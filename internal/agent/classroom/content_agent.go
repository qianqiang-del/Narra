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

// contentInput 是内容专家的输入：本页分层上下文、证据包与修订反馈。
type contentInput struct {
	Page     PageContext
	Evidence *EvidenceBundle
	Revision string
	Feedback string
}

// blocksOutput 是内容专家的原始输出结构。
type blocksOutput struct {
	Blocks []contentBlock `json:"blocks"`
}

// buildContentChain 建内容专家的执行单元：拼提示词 → 模型 → 解析成内容块。
func buildContentChain(ctx context.Context, rt *runtime) (compose.Runnable[*contentInput, []contentBlock], error) {
	chain := compose.NewChain[*contentInput, []contentBlock]()
	chain.AppendLambda(compose.InvokableLambda(func(_ context.Context, in *contentInput) ([]*schema.Message, error) {
		system, ok := agent.BuildTaskPrompt(agent.TaskScene)
		if !ok {
			return nil, fmt.Errorf("场景内容提示词未注册")
		}
		return []*schema.Message{schema.SystemMessage(system), schema.UserMessage(contentUserPrompt(in))}, nil
	})).AppendChatModel(rt.chatModel).AppendLambda(compose.InvokableLambda(func(_ context.Context, msg *schema.Message) ([]contentBlock, error) {
		var out blocksOutput
		if err := unmarshalLoose(msg.Content, &out); err != nil {
			return nil, malformedOutput(err)
		}
		return out.Blocks, nil
	}))
	return chain.Compile(ctx)
}

// generateContent 生成一页内容并校验，不合规就带着提示重跑同一条 Chain。
func generateContent(ctx context.Context, rt *runtime, budget *pageBudget, in *contentInput, sceneType string) ([]contentBlock, error) {
	chain, err := buildContentChain(ctx, rt)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt <= maxValidateRetry; attempt++ {
		if !budget.trySpend() {
			lastErr = fmt.Errorf("这一页的模型调用预算已用尽")
			break
		}
		blocks, runErr := chain.Invoke(ctx, in)
		if runErr != nil {
			lastErr = runErr
			if !retryableModelError(runErr) && !isMalformedOutput(runErr) {
				break
			}
			in.Feedback = outputFeedback(runErr)
			continue
		}
		validated, validateErr := validateBlocks(blocks, sceneType)
		if validateErr == nil {
			validateErr = checkContentSize(validated)
		}
		if validateErr == nil {
			return validated, nil
		}
		lastErr = validateErr
		in.Feedback = outputFeedback(validateErr)
	}
	return nil, lastErr
}

// checkContentSize 兜住一页内容的体积，见 maxPageContentBytes。
func checkContentSize(blocks []contentBlock) error {
	raw, err := json.Marshal(blocks)
	if err != nil {
		return fmt.Errorf("内容块编码失败: %w", err)
	}
	if len(raw) > maxPageContentBytes {
		return fmt.Errorf("页面内容过大（%d 字节，上限 %d 字节）：把示例代码、内联 HTML 和重复说明压缩掉",
			len(raw), maxPageContentBytes)
	}
	return nil
}

// contentUserPrompt 拼内容专家的用户提示词。
func contentUserPrompt(in *contentInput) string {
	var builder strings.Builder
	in.Page.writePrompt(&builder)
	writeEvidence(&builder, in.Evidence)
	writeRevision(&builder, in.Revision, in.Feedback)
	return builder.String()
}

// writeEvidence 把证据包写进提示词；没有证据时明确说明，免得模型以为漏发了。
func writeEvidence(builder *strings.Builder, evidence *EvidenceBundle) {
	if evidence == nil || (len(evidence.Items) == 0 && strings.TrimSpace(evidence.Summary) == "") {
		builder.WriteString("\n## 参考资料\n本次没有可用的外部资料，按已有知识写，不要编造具体数字与版本号。\n")
		return
	}
	builder.WriteString("\n## 参考资料\n")
	if summary := strings.TrimSpace(evidence.Summary); summary != "" {
		fmt.Fprintf(builder, "摘要：%s\n", summary)
	}
	for _, item := range evidence.Items {
		source := strings.TrimSpace(item.Source)
		if source == "" {
			source = "未标注来源"
		}
		fmt.Fprintf(builder, "- %s（来源：%s）\n", item.Claim, source)
	}
	if len(evidence.Conflicts) > 0 {
		fmt.Fprintf(builder, "资料之间有冲突的地方：%s\n", strings.Join(evidence.Conflicts, "；"))
	}
	builder.WriteString("引用资料时只写它确实说过的事实，资料没提的不要补。\n")
}
