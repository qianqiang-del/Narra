package classroom

import (
	"fmt"
	"strings"
)

// validateQuiz 校验测验页：至少一道可作答的题，选项不重复，答案在选项里。
func validateQuiz(blocks []contentBlock) error {
	questions := 0
	for _, block := range blocks {
		if block.Type != blockTypeQuiz {
			continue
		}
		if block.Interaction == nil {
			return fmt.Errorf("quiz 内容块 %q 缺少 interaction", block.Key)
		}
		options := normalizeOptions(block.Interaction.Options)
		if len(options) < 2 {
			return fmt.Errorf("quiz 内容块 %q 至少要有两个不重复的选项", block.Key)
		}
		answer := strings.TrimSpace(block.Interaction.Answer)
		if answer == "" {
			return fmt.Errorf("quiz 内容块 %q 缺少正确答案", block.Key)
		}
		if !containsText(options, answer) {
			return fmt.Errorf("quiz 内容块 %q 的答案 %q 不在选项里", block.Key, answer)
		}
		questions++
	}
	if questions == 0 {
		return fmt.Errorf("quiz 页面至少要有一个 type 为 quiz 且带 options 的内容块")
	}
	return nil
}

// normalizeOptions 去掉选项首尾空白、丢弃空项与重复项。
func normalizeOptions(options []string) []string {
	result := make([]string, 0, len(options))
	seen := make(map[string]struct{}, len(options))
	for _, option := range options {
		trimmed := strings.TrimSpace(option)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

// containsText 判断文本是否在列表里。
func containsText(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
