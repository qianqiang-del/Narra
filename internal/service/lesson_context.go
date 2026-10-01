package service

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"narra/internal/model/entity"
)

const (
	lessonPageSummaryLimit = 500
	lessonSelectedLimit    = 2500
	lessonNarrationLimit   = 1200
	lessonTotalLimit       = 12000
)

var pageNumberPattern = regexp.MustCompile(`第([0-9一二三四五六七八九十]+)页`)

// selectLessonScene 优先选用户明确提到的页，否则选浏览器当前页。
// scenes 必须已经按课堂 ID 读取；不属于这门课的当前页 ID 会被拒绝。
func selectLessonScene(scenes []entity.Scene, currentSceneID uint64, question string) (*entity.Scene, error) {
	pages := make([]entity.Scene, 0, len(scenes))
	var current *entity.Scene
	for _, scene := range scenes {
		if scene.Type == entity.SceneTypeComplete {
			continue
		}
		pages = append(pages, scene)
		if currentSceneID != 0 && scene.ID == currentSceneID {
			current = &pages[len(pages)-1]
		}
	}
	if currentSceneID != 0 && current == nil {
		return nil, fmt.Errorf("当前课件页不属于这门课堂")
	}
	if match := pageNumberPattern.FindStringSubmatch(question); len(match) == 2 {
		pageNo := parsePageNumber(match[1])
		if pageNo > 0 && pageNo <= len(pages) {
			return &pages[pageNo-1], nil
		}
		return nil, nil
	}
	return current, nil
}

func parsePageNumber(value string) int {
	if number, err := strconv.Atoi(value); err == nil {
		return number
	}
	digits := map[rune]int{'一': 1, '二': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	runes := []rune(value)
	if len(runes) == 1 {
		if runes[0] == '十' {
			return 10
		}
		return digits[runes[0]]
	}
	if len(runes) == 2 && runes[0] == '十' {
		return 10 + digits[runes[1]]
	}
	if len(runes) == 2 && runes[1] == '十' {
		return digits[runes[0]] * 10
	}
	if len(runes) == 3 && runes[1] == '十' {
		return digits[runes[0]]*10 + digits[runes[2]]
	}
	return 0
}

type lessonBlock struct {
	Type        string `json:"type"`
	Content     string `json:"content"`
	Interaction *struct {
		Options []string `json:"options"`
		Answer  string   `json:"answer"`
		Config  struct {
			Explanation string `json:"explanation"`
		} `json:"config"`
	} `json:"interaction"`
}

func sceneText(scene entity.Scene) string {
	var content struct {
		Blocks []lessonBlock `json:"blocks"`
	}
	if json.Unmarshal(scene.Content, &content) != nil {
		return ""
	}
	parts := make([]string, 0, len(content.Blocks))
	for _, block := range content.Blocks {
		if value := strings.TrimSpace(block.Content); value != "" {
			parts = append(parts, value)
		}
		if block.Interaction != nil {
			if len(block.Interaction.Options) > 0 {
				parts = append(parts, "选项："+strings.Join(block.Interaction.Options, "、"))
			}
			if block.Interaction.Answer != "" {
				parts = append(parts, "答案："+block.Interaction.Answer)
			}
			if block.Interaction.Config.Explanation != "" {
				parts = append(parts, "解析："+block.Interaction.Config.Explanation)
			}
		}
	}
	if scene.InteractiveHTML != "" {
		root, err := html.Parse(strings.NewReader(scene.InteractiveHTML))
		if err == nil {
			var visible []string
			var walk func(*html.Node)
			walk = func(node *html.Node) {
				if node.Type == html.ElementNode {
					switch node.Data {
					case "script", "style", "noscript", "template", "svg":
						return
					}
				}
				if node.Type == html.TextNode {
					if text := strings.TrimSpace(node.Data); text != "" {
						visible = append(visible, text)
					}
				}
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					walk(child)
				}
			}
			walk(root)
			parts = append(parts, strings.Join(visible, "；"))
		}
	}
	return strings.Join(parts, "；")
}

func limitRunes(value string, max int) string {
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	return string([]rune(value)[:max]) + "…"
}

// formatLessonMaterial 保留全课的有序摘要，并优先放入重点页的详细内容。
func formatLessonMaterial(scenes []entity.Scene, selected *entity.Scene, narration []entity.SceneSegment) string {
	var material strings.Builder
	material.WriteString("以下是本课堂已生成的课件资料，按页面顺序排列。未就绪的页面没有可引用的正文。\n")
	if selected != nil {
		selectedPageNo := 0
		for _, scene := range scenes {
			if scene.Type == entity.SceneTypeComplete {
				continue
			}
			selectedPageNo++
			if scene.ID != selected.ID {
				continue
			}
			material.WriteString(fmt.Sprintf("重点页：第%d页《%s》：", selectedPageNo, scene.Title))
			if scene.Status == entity.SceneStatusReady {
				material.WriteString(limitRunes(sceneText(scene), lessonSelectedLimit))
			} else {
				material.WriteString("尚未生成完成")
			}
			material.WriteString("\n")
			break
		}
		if selected.Status == entity.SceneStatusReady && len(narration) > 0 {
			parts := make([]string, 0, len(narration))
			for _, segment := range narration {
				if segment.Status == entity.SceneSegmentStatusReady && strings.TrimSpace(segment.Text) != "" {
					parts = append(parts, strings.TrimSpace(segment.Text))
				}
			}
			if len(parts) > 0 {
				material.WriteString("重点页教师讲解：" + limitRunes(strings.Join(parts, "；"), lessonNarrationLimit) + "\n")
			}
		}
	}
	pageNo := 0
	for _, scene := range scenes {
		if scene.Type == entity.SceneTypeComplete {
			continue
		}
		pageNo++
		line := fmt.Sprintf("第%d页《%s》：", pageNo, scene.Title)
		if scene.Status != entity.SceneStatusReady {
			line += "尚未生成完成"
		} else {
			text := sceneText(scene)
			if text == "" {
				line += "暂无可读取的页面正文"
			} else {
				line += limitRunes(text, lessonPageSummaryLimit)
			}
		}
		if utf8.RuneCountInString(material.String())+utf8.RuneCountInString(line) > lessonTotalLimit {
			material.WriteString("其余页面因长度限制未列出。\n")
			break
		}
		material.WriteString(line + "\n")
	}
	return limitRunes(material.String(), lessonTotalLimit)
}
