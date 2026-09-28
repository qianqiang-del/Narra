package classroom

import "fmt"

// validatePBL 校验项目式页面：要有任务清单或分栏，且不出现浏览器块。
func validatePBL(blocks []contentBlock) error {
	if !hasBlockType(blocks, blockTypeColumns, blockTypeListItem) {
		return fmt.Errorf("pbl 页面至少要有 columns 或 list-item 内容块来表达任务与产出")
	}
	for _, block := range blocks {
		if block.Type == blockTypeBrowser {
			return fmt.Errorf("pbl 页面不能包含 browser 内容块，内容块 %q 违规", block.Key)
		}
	}
	return nil
}
