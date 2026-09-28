package classroom

import (
	"fmt"
	"strings"

	"narra/internal/model/entity"
)

// validateBlocks 校验一页内容块并就地归一化文本字段，最后按场景类型走各自的专属规则。
//
// 通用规则（四种场景共用）在这里，类型专属规则在 validation_<type>.go。
func validateBlocks(blocks []contentBlock, sceneType string) ([]contentBlock, error) {
	if len(blocks) == 0 {
		return nil, fmt.Errorf("这一页一个内容块都没有")
	}
	keys := make(map[string]struct{}, len(blocks))
	for index := range blocks {
		block := &blocks[index]
		block.Key = truncateRunes(strings.TrimSpace(block.Key), maxContentKeyRunes)
		block.Type = strings.TrimSpace(block.Type)
		block.Content = strings.TrimSpace(block.Content)
		if block.Key == "" || block.Content == "" {
			return nil, fmt.Errorf("第 %d 个内容块不完整", index+1)
		}
		if _, exists := keys[block.Key]; exists {
			return nil, fmt.Errorf("内容块 key %q 重复", block.Key)
		}
		if _, ok := allowedBlockTypes[block.Type]; !ok {
			return nil, fmt.Errorf("内容块类型 %q 无效", block.Type)
		}
		keys[block.Key] = struct{}{}
		if err := validateInteraction(*block); err != nil {
			return nil, err
		}
	}

	var err error
	switch sceneType {
	case entity.SceneTypeSlide:
		err = validateSlide(blocks)
	case entity.SceneTypeQuiz:
		err = validateQuiz(blocks)
	case entity.SceneTypePBL:
		err = validatePBL(blocks)
	case entity.SceneTypeInteractive:
		err = validateInteractive(blocks)
	default:
		err = fmt.Errorf("场景类型 %q 没有对应的校验规则", sceneType)
	}
	if err != nil {
		return nil, err
	}
	return blocks, nil
}

// validateInteraction 校验一个块的交互配置。
func validateInteraction(block contentBlock) error {
	if block.Interaction == nil {
		return nil
	}
	if strings.TrimSpace(block.Interaction.Kind) == "" {
		return fmt.Errorf("内容块 %q 的 interaction.kind 不能为空", block.Key)
	}
	for _, control := range block.Interaction.Controls {
		if strings.TrimSpace(control.Name) == "" || strings.TrimSpace(control.Type) == "" || control.Default == nil {
			return fmt.Errorf("内容块 %q 的交互控件缺少 name、type 或 default", block.Key)
		}
		switch control.Type {
		case "select":
			if len(control.Options) == 0 {
				return fmt.Errorf("内容块 %q 的 select 控件缺少 options", block.Key)
			}
		case "range":
			if control.Min == nil || control.Max == nil || control.Step == nil {
				return fmt.Errorf("内容块 %q 的 range 控件缺少 min、max 或 step", block.Key)
			}
		}
	}
	return nil
}

// hasBlockType 判断这一页有没有某类内容块。
func hasBlockType(blocks []contentBlock, types ...string) bool {
	for _, block := range blocks {
		for _, want := range types {
			if block.Type == want {
				return true
			}
		}
	}
	return false
}

// validateNarration 校验讲解稿与内容块的对应关系，并按内容块顺序重排。
func validateNarration(items []narrationSegment, blocks []contentBlock) ([]narrationSegment, error) {
	byKey := make(map[string]string, len(items))
	for _, item := range items {
		if _, exists := byKey[item.ContentKey]; exists {
			return nil, fmt.Errorf("内容块 %q 有重复讲解", item.ContentKey)
		}
		if strings.TrimSpace(item.Text) == "" {
			return nil, fmt.Errorf("内容块 %q 的讲解为空", item.ContentKey)
		}
		byKey[item.ContentKey] = strings.TrimSpace(item.Text)
	}
	result := make([]narrationSegment, 0, len(blocks))
	for _, block := range blocks {
		text, ok := byKey[block.Key]
		if !ok {
			return nil, fmt.Errorf("内容块 %q 缺少讲解", block.Key)
		}
		result = append(result, narrationSegment{ContentKey: block.Key, Text: text})
	}
	return result, nil
}
