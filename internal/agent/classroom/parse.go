package classroom

import (
	"encoding/json"
	"fmt"
	"strings"

	"narra/internal/model/entity"
)

// 与数据库列对应的长度上限，超长按字符截断。
const (
	maxClassroomTitleRunes = 200 // classrooms.title
	maxSceneTitleRunes     = 200 // scenes.title
	maxContentKeyRunes     = 120 // scene_segments.content_key
)

// block 的 type 与 internal/model/entity/scene.go 的注释一一对应。
const (
	blockTypeHeading   = "heading"
	blockTypeParagraph = "paragraph"
	blockTypeListItem  = "list-item"
	blockTypeCallout   = "callout"
	blockTypeCode      = "code"
	blockTypeQuiz      = "quiz"
	blockTypeBrowser   = "browser"
	blockTypeColumns   = "columns"
)

var allowedBlockTypes = map[string]struct{}{
	blockTypeHeading:   {},
	blockTypeParagraph: {},
	blockTypeListItem:  {},
	blockTypeCallout:   {},
	blockTypeCode:      {},
	blockTypeQuiz:      {},
	blockTypeBrowser:   {},
	blockTypeColumns:   {},
}

// allowedSceneTypes 不含 complete，完成页由代码追加。
var allowedSceneTypes = map[string]struct{}{
	entity.SceneTypeSlide:       {},
	entity.SceneTypeQuiz:        {},
	entity.SceneTypeInteractive: {},
	entity.SceneTypePBL:         {},
}

// outlinePlan 是大纲的解析产物。场景顺序即切片顺序。
type outlinePlan struct {
	Title  string      `json:"title"`
	Scenes []scenePlan `json:"scenes"`
}

// scenePlan 是一个场景在大纲里的样子。
//
// Brief 不落库，只在内存里传给段二；SceneID 是段一建行之后回填的，段二靠它更新对应场景。
type scenePlan struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	Brief   string `json:"brief"`
	SceneID uint64 `json:"-"`
}

// sceneContent 是单场景的解析产物。
type sceneContent struct {
	Blocks    []contentBlock     `json:"blocks"`
	Narration []narrationSegment `json:"narration"`
}

type contentBlock struct {
	Key     string `json:"key"`
	Type    string `json:"type"`
	Content string `json:"content"`
}

type narrationSegment struct {
	ContentKey string `json:"content_key"`
	Text       string `json:"text"`
}

// parseOutline 解析并校验大纲。
func parseOutline(raw string) (*outlinePlan, error) {
	var plan outlinePlan
	if err := unmarshalLoose(raw, &plan); err != nil {
		return nil, fmt.Errorf("大纲不是合法 JSON: %w", err)
	}

	plan.Title = truncateRunes(strings.TrimSpace(plan.Title), maxClassroomTitleRunes)
	if plan.Title == "" {
		return nil, fmt.Errorf("大纲缺少课程标题")
	}
	if len(plan.Scenes) == 0 {
		return nil, fmt.Errorf("大纲里一页都没有")
	}

	for index := range plan.Scenes {
		scene := &plan.Scenes[index]
		scene.Type = strings.TrimSpace(scene.Type)
		if _, ok := allowedSceneTypes[scene.Type]; !ok {
			return nil, fmt.Errorf("第 %d 页的类型 %q 不在允许范围内（slide / quiz / interactive / pbl）", index+1, scene.Type)
		}
		scene.Title = truncateRunes(strings.TrimSpace(scene.Title), maxSceneTitleRunes)
		if scene.Title == "" {
			return nil, fmt.Errorf("第 %d 页缺少标题", index+1)
		}
		scene.Brief = strings.TrimSpace(scene.Brief)
		if scene.Brief == "" {
			scene.Brief = scene.Title
		}
	}
	return &plan, nil
}

// parseSceneContent 解析并校验一个场景的内容块与讲稿。
func parseSceneContent(raw string) (*sceneContent, error) {
	var content sceneContent
	if err := unmarshalLoose(raw, &content); err != nil {
		return nil, fmt.Errorf("场景内容不是合法 JSON: %w", err)
	}
	if len(content.Blocks) == 0 {
		return nil, fmt.Errorf("这一页一个内容块都没有")
	}
	if len(content.Narration) == 0 {
		return nil, fmt.Errorf("这一页没有讲解稿")
	}

	keys := make(map[string]struct{}, len(content.Blocks))
	for index := range content.Blocks {
		block := &content.Blocks[index]
		block.Key = truncateRunes(strings.TrimSpace(block.Key), maxContentKeyRunes)
		if block.Key == "" {
			return nil, fmt.Errorf("第 %d 个内容块缺少 key", index+1)
		}
		if _, dup := keys[block.Key]; dup {
			return nil, fmt.Errorf("内容块的 key %q 重复了", block.Key)
		}
		keys[block.Key] = struct{}{}

		block.Type = strings.TrimSpace(block.Type)
		if _, ok := allowedBlockTypes[block.Type]; !ok {
			return nil, fmt.Errorf("第 %d 个内容块的类型 %q 不在允许范围内", index+1, block.Type)
		}
		block.Content = strings.TrimSpace(block.Content)
		if block.Content == "" {
			return nil, fmt.Errorf("第 %d 个内容块没有内容", index+1)
		}
	}

	texts := make(map[string]string, len(content.Narration))
	for index, segment := range content.Narration {
		key := truncateRunes(strings.TrimSpace(segment.ContentKey), maxContentKeyRunes)
		if _, ok := keys[key]; !ok {
			return nil, fmt.Errorf("第 %d 段讲解的 content_key %q 在内容块里找不到", index+1, key)
		}
		if _, dup := texts[key]; dup {
			return nil, fmt.Errorf("内容块 %q 有多段讲解", key)
		}
		text := strings.TrimSpace(segment.Text)
		if text == "" {
			return nil, fmt.Errorf("第 %d 段讲解是空的", index+1)
		}
		texts[key] = text
	}

	// 讲稿按 blocks 顺序重排：scene_segments.sort_order 要与页面高亮顺序一致。
	narration := make([]narrationSegment, 0, len(texts))
	for _, block := range content.Blocks {
		if text, ok := texts[block.Key]; ok {
			narration = append(narration, narrationSegment{ContentKey: block.Key, Text: text})
		}
	}
	content.Narration = narration
	return &content, nil
}

// unmarshalLoose 剥掉可能的代码围栏与前后废话，再反序列化。
func unmarshalLoose(raw string, target any) error {
	text := extractJSON(raw)
	if text == "" {
		return fmt.Errorf("没找到 JSON")
	}
	return json.Unmarshal([]byte(text), target)
}

// extractJSON 取第一个 { 到最后一个 } 之间的内容。
func extractJSON(raw string) string {
	text := strings.TrimSpace(raw)
	if strings.HasPrefix(text, "```") {
		if newline := strings.IndexByte(text, '\n'); newline >= 0 {
			text = text[newline+1:]
		}
		if end := strings.LastIndex(text, "```"); end >= 0 {
			text = text[:end]
		}
		text = strings.TrimSpace(text)
	}

	start := strings.IndexByte(text, '{')
	end := strings.LastIndexByte(text, '}')
	if start < 0 || end <= start {
		return ""
	}
	return text[start : end+1]
}

// truncateRunes 按字符截断，避免切坏汉字。
func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}
