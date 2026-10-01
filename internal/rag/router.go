package rag

import "context"

// router.go 是切分入口的路由：先判定文档类型，再交给对应的切分器。
//
// 它保证"两条收录链路（上传文件、直接录入）共用同一份判定"，也保证判定发生在
// 内容确定之后、装填之前 —— 从库里的旧正文重新切分时会重新判定，不需要重新解析。
//
// 路由只分两支：代码走 code.go 的结构切分；文档与普通文本仍走
// transformer.go 的 MarkdownChunker/Split（行为不变），只是在结果上补一个
// content_type。判定规则与置信度见 classify.go 的文件头。
func splitDocument(ctx context.Context, content string, hint DocumentHint, options ChunkOptions) ([]Chunk, error) {
	classification := ClassifyDocument(content, hint)
	if classification.Kind == KindCode {
		return SplitCode(content, classification.Language, options), nil
	}

	chunks, err := splitMarkdown(ctx, content, options)
	if err != nil {
		return nil, err
	}
	for index := range chunks {
		chunks[index].ContentType = string(classification.Kind)
	}
	return chunks, nil
}
