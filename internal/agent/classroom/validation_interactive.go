package classroom

import "fmt"

// validateInteractive 校验交互页：至少一个可操作块，且它带交互配置。
func validateInteractive(blocks []contentBlock) error {
	for _, block := range blocks {
		if block.Type != blockTypeBrowser && block.Type != blockTypeInteractive {
			continue
		}
		if block.Interaction != nil {
			return nil
		}
	}
	return fmt.Errorf("interactive 页面至少要有一个带 interaction 的 browser 或 interactive 内容块")
}
