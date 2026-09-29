package classroom

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"narra/internal/agent"
	"narra/internal/model/entity"
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

// pageContent 是一页的模型产物：内容块，以及交互页独有的完整 HTML 文档。
type pageContent struct {
	Blocks []contentBlock
	HTML   string
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
//
// 交互页走另一条路：它的正文是一份完整 HTML 文档，不是内容块。
func generateContent(ctx context.Context, rt *runtime, budget *pageBudget, in *contentInput, sceneType string) (pageContent, error) {
	if sceneType == entity.SceneTypeInteractive {
		return generateInteractiveContent(ctx, rt, budget, in)
	}
	chain, err := buildContentChain(ctx, rt)
	if err != nil {
		return pageContent{}, err
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
			return pageContent{Blocks: validated}, nil
		}
		lastErr = validateErr
		in.Feedback = outputFeedback(validateErr)
	}
	return pageContent{}, lastErr
}

// buildInteractiveChain 建交互页的执行单元：拼提示词 → 流式生成 → 原样取回完整 HTML 文档。
//
// 走流式而不是 AppendChatModel：交互页输出是一份完整 HTML，属长回答，非流式会把
// "发请求 + 读完整个响应体"一起框进 Provider 超时（默认 60s），长文档常在读 body 时被掐断
// （"读取服务响应失败: context deadline exceeded"）。流式没有那个固定死线，时限只由 ctx 决定。
func buildInteractiveChain(ctx context.Context, rt *runtime) (compose.Runnable[*contentInput, string], error) {
	chain := compose.NewChain[*contentInput, string]()
	chain.AppendLambda(compose.InvokableLambda(func(_ context.Context, in *contentInput) ([]*schema.Message, error) {
		system, ok := agent.BuildTaskPrompt(agent.TaskSceneInteractive)
		if !ok {
			return nil, fmt.Errorf("交互页面提示词未注册")
		}
		return []*schema.Message{schema.SystemMessage(system), schema.UserMessage(contentUserPrompt(in))}, nil
	})).AppendLambda(compose.InvokableLambda(func(ctx context.Context, messages []*schema.Message) (string, error) {
		return rt.streamCompletion(ctx, messages)
	}))
	return chain.Compile(ctx)
}

// generateInteractiveContent 生成一页交互 HTML 并校验，不合规就带着提示重跑。
//
// 内容块里只留一个讲解锚点：讲稿、段落与语音的既有流程因此完全不用改，
// 交互页的正文（HTML）走 scenes.interactive_html 那一列。
func generateInteractiveContent(ctx context.Context, rt *runtime, budget *pageBudget, in *contentInput) (pageContent, error) {
	chain, err := buildInteractiveChain(ctx, rt)
	if err != nil {
		return pageContent{}, err
	}
	anchor := interactiveAnchor(in.Page.Current)
	var lastErr error
	for attempt := 0; attempt <= maxValidateRetry; attempt++ {
		if !budget.trySpend() {
			lastErr = fmt.Errorf("这一页的模型调用预算已用尽")
			break
		}
		raw, runErr := chain.Invoke(ctx, in)
		if runErr != nil {
			lastErr = runErr
			if !retryableModelError(runErr) {
				break
			}
			in.Feedback = outputFeedback(runErr)
			continue
		}
		document, validateErr := extractHTMLDocument(raw)
		if validateErr == nil {
			validateErr = validateHTMLDocument(document)
		}
		if validateErr == nil {
			return pageContent{Blocks: []contentBlock{anchor}, HTML: document}, nil
		}
		lastErr = validateErr
		in.Feedback = htmlFeedback(validateErr)
	}
	return pageContent{}, lastErr
}

// interactiveAnchor 造交互页在内容块里的讲解锚点，讲稿段落挂在它的 key 上。
//
// 用 paragraph 而不是 interactive：交互页的正文在 HTML 里，这一块只是讲稿的落点，
// paragraph 能让还没接 iframe 的前端至少把这一页要讲什么显示出来，不至于整页空白。
func interactiveAnchor(page PlanPage) contentBlock {
	content := strings.TrimSpace(page.Brief)
	if content == "" {
		content = strings.TrimSpace(page.Title)
	}
	if content == "" {
		content = "在这一页里动手操作，观察结果怎么随输入变化。"
	}
	return contentBlock{
		Key:     interactiveAnchorKey,
		Type:    blockTypeParagraph,
		Content: truncateRunes(content, maxAnchorRunes),
	}
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
