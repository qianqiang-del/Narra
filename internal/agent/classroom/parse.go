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
	blockTypeHeading     = "heading"
	blockTypeParagraph   = "paragraph"
	blockTypeListItem    = "list-item"
	blockTypeCallout     = "callout"
	blockTypeCode        = "code"
	blockTypeQuiz        = "quiz"
	blockTypeBrowser     = "browser"
	blockTypeInteractive = "interactive"
	blockTypeColumns     = "columns"
)

var allowedBlockTypes = map[string]struct{}{
	blockTypeHeading:     {},
	blockTypeParagraph:   {},
	blockTypeListItem:    {},
	blockTypeCallout:     {},
	blockTypeCode:        {},
	blockTypeQuiz:        {},
	blockTypeBrowser:     {},
	blockTypeInteractive: {},
	blockTypeColumns:     {},
}

// allowedSceneTypes 不含 complete，完成页由代码追加。
var allowedSceneTypes = map[string]struct{}{
	entity.SceneTypeSlide:       {},
	entity.SceneTypeQuiz:        {},
	entity.SceneTypeInteractive: {},
	entity.SceneTypePBL:         {},
}

type contentBlock struct {
	Key         string             `json:"key"`
	Type        string             `json:"type"`
	Content     string             `json:"content"`
	Interaction *interactionConfig `json:"interaction,omitempty"`
}

type interactionConfig struct {
	Kind     string               `json:"kind"`
	Controls []interactionControl `json:"controls,omitempty"`
	Options  []string             `json:"options,omitempty"`
	Answer   string               `json:"answer,omitempty"`
	Config   map[string]any       `json:"config,omitempty"`
}

type interactionControl struct {
	Name    string         `json:"name"`
	Type    string         `json:"type"`
	Default any            `json:"default,omitempty"`
	Min     *float64       `json:"min,omitempty"`
	Max     *float64       `json:"max,omitempty"`
	Step    *float64       `json:"step,omitempty"`
	Options []string       `json:"options,omitempty"`
	Config  map[string]any `json:"config,omitempty"`
}

type narrationSegment struct {
	ContentKey string `json:"content_key"`
	Text       string `json:"text"`
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

// unmarshalArrayLoose 剥掉可能的代码围栏与前后废话，再反序列化一个 JSON 数组。
func unmarshalArrayLoose(raw string, target any) error {
	text := strings.TrimSpace(raw)
	if strings.HasPrefix(text, "```") {
		if newline := strings.IndexByte(text, '\n'); newline >= 0 {
			text = text[newline+1:]
		}
		if end := strings.LastIndex(text, "```"); end >= 0 {
			text = text[:end]
		}
	}
	start, end := strings.IndexByte(text, '['), strings.LastIndexByte(text, ']')
	if start < 0 || end <= start {
		return fmt.Errorf("没找到 JSON 数组")
	}
	return json.Unmarshal([]byte(text[start:end+1]), target)
}

// truncateRunes 按字符截断，避免切坏汉字。
func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}
