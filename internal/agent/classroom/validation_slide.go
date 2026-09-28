package classroom

import "fmt"

// validateSlide 校验讲解页：要有标题块，且不能出现浏览器块与可操作控件。
func validateSlide(blocks []contentBlock) error {
	if !hasBlockType(blocks, blockTypeHeading) {
		return fmt.Errorf("slide 页面至少要有一个 heading 内容块")
	}
	for _, block := range blocks {
		if block.Type == blockTypeBrowser || block.Type == blockTypeInteractive {
			return fmt.Errorf("slide 页面不能包含 %s 内容块", block.Type)
		}
		if block.Interaction != nil && len(block.Interaction.Controls) > 0 {
			return fmt.Errorf("slide 页面不能包含带控件的交互，内容块 %q 违规", block.Key)
		}
	}
	return nil
}
