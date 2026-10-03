package classroom

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"

	"narra/internal/material"
)

// 摘要提示词：只服务于排课，说清"这节/这份材料讲什么"，不做价值判断、不展开讲解。
const (
	outlineSectionSystemPrompt = `你是课程材料分析助手。用户会给你若干节材料的节选，请为每一节写一句中文摘要，说明这一节讲什么、重点是什么。
要求：
1. 只依据给出的节选，不要编造；
2. 每句 40 字以内，直接写内容，不要写"本节介绍了"这类套话；
3. 只输出 JSON 数组，数组长度与输入的节数一致，不要输出任何解释。`

	outlineDocumentSystemPrompt = `你是课程材料分析助手。用户会给你一份材料的章节摘要或正文节选，请用 1-2 句话概括这份材料整体讲什么、重点是什么。
要求：
1. 只依据给出的内容，不要编造；
2. 80 字以内，直接写内容；
3. 只输出摘要文字，不要输出 JSON、标题或解释。`
)

// materialSummarizer 用本课模型实现 material.Summarizer。
type materialSummarizer struct {
	rt *runtime
}

// newMaterialSummarizer 构造材料摘要器；rt 为空时返回 nil，调用方退回代码目录。
func newMaterialSummarizer(rt *runtime) material.Summarizer {
	if rt == nil {
		return nil
	}
	return &materialSummarizer{rt: rt}
}

// SummarizeSections 见 material.Summarizer 接口注释。
func (s *materialSummarizer) SummarizeSections(ctx context.Context, documentName string, sections []material.SectionSample) ([]string, error) {
	if len(sections) == 0 {
		return nil, nil
	}
	var input strings.Builder
	fmt.Fprintf(&input, "材料：%s\n", documentName)
	for index, section := range sections {
		fmt.Fprintf(&input, "\n## %d. %s\n%s\n", index+1, section.Path, section.Content)
	}
	messages := []*schema.Message{
		schema.SystemMessage(outlineSectionSystemPrompt),
		schema.UserMessage(input.String()),
	}
	message, err := invokeWithRetryIf(ctx, maxTransientRetry, retryableModelError, func() (*schema.Message, error) {
		return s.rt.generateText(ctx, messages)
	})
	if err != nil {
		return nil, err
	}
	var summaries []string
	if err := unmarshalArrayLoose(message.Content, &summaries); err != nil {
		return nil, fmt.Errorf("解析章节摘要失败: %w", err)
	}
	if len(summaries) != len(sections) {
		return nil, fmt.Errorf("章节摘要条数 %d 与章节数 %d 不一致", len(summaries), len(sections))
	}
	return summaries, nil
}

// SummarizeDocument 见 material.Summarizer 接口注释。
func (s *materialSummarizer) SummarizeDocument(ctx context.Context, documentName string, sections []material.SectionSample) (string, error) {
	var input strings.Builder
	fmt.Fprintf(&input, "材料：%s\n", documentName)
	for _, section := range sections {
		if path := strings.TrimSpace(section.Path); path != "" {
			fmt.Fprintf(&input, "- %s：%s\n", path, section.Content)
			continue
		}
		input.WriteString(section.Content)
		input.WriteString("\n")
	}
	messages := []*schema.Message{
		schema.SystemMessage(outlineDocumentSystemPrompt),
		schema.UserMessage(strings.TrimSpace(input.String())),
	}
	message, err := invokeWithRetryIf(ctx, maxTransientRetry, retryableModelError, func() (*schema.Message, error) {
		return s.rt.generateText(ctx, messages)
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(message.Content), nil
}
