package classroom

import (
	"encoding/json"
	"fmt"
	"strings"
)

// renderBlocksForReview 把最终内容块压成审核要读的文本。
//
// 只换排版，不删信息：编号、类型、key、正文全留，测验块另列选项、答案与其他配置。
// 相比整段 JSON，省掉的是引号、花括号与重复的字段名，审核判内容与测验正确性所需的一条不少。
func renderBlocksForReview(blocks []contentBlock) string {
	var builder strings.Builder
	for index, block := range blocks {
		fmt.Fprintf(&builder, "%d. [%s] key=%s\n", index+1, block.Type, block.Key)
		if content := strings.TrimSpace(block.Content); content != "" {
			builder.WriteString(content)
			builder.WriteByte('\n')
		}
		if interaction := block.Interaction; interaction != nil {
			if kind := strings.TrimSpace(interaction.Kind); kind != "" {
				fmt.Fprintf(&builder, "   交互类型：%s\n", kind)
			}
			if len(interaction.Options) > 0 {
				fmt.Fprintf(&builder, "   选项：%s\n", strings.Join(interaction.Options, " ｜ "))
			}
			if answer := strings.TrimSpace(interaction.Answer); answer != "" {
				fmt.Fprintf(&builder, "   答案：%s\n", answer)
			}
			if len(interaction.Config) > 0 {
				if raw, err := json.Marshal(interaction.Config); err == nil {
					fmt.Fprintf(&builder, "   其他配置：%s\n", raw)
				}
			}
		}
	}
	return strings.TrimRight(builder.String(), "\n")
}

// renderNarrationForReview 把最终讲稿压成审核要读的文本。
//
// 每段给出编号、对应的内容块 key 与讲解文本：key 保留是为了让审核能核对"讲稿与内容块一一对应"。
func renderNarrationForReview(segments []narrationSegment) string {
	var builder strings.Builder
	for index, segment := range segments {
		fmt.Fprintf(&builder, "%d. key=%s\n", index+1, segment.ContentKey)
		if text := strings.TrimSpace(segment.Text); text != "" {
			builder.WriteString(text)
			builder.WriteByte('\n')
		}
	}
	return strings.TrimRight(builder.String(), "\n")
}
