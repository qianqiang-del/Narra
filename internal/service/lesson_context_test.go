package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"narra/internal/model/entity"
)

func lessonScenes() []entity.Scene {
	return []entity.Scene{
		{BaseModel: entity.BaseModel{ID: 11}, ClassroomID: 1, SortOrder: 0, Title: "认识 Agent", Status: entity.SceneStatusReady, Content: json.RawMessage(`{"blocks":[{"key":"a","type":"paragraph","content":"Agent 能感知环境并采取行动。"}]}`)},
		{BaseModel: entity.BaseModel{ID: 12}, ClassroomID: 1, SortOrder: 1, Title: "工具与反馈", Status: entity.SceneStatusReady, Content: json.RawMessage(`{"blocks":[{"key":"b","type":"paragraph","content":"工具调用后要观察结果，再调整下一步。"}]}`)},
		{BaseModel: entity.BaseModel{ID: 13}, ClassroomID: 1, SortOrder: 2, Title: "待生成练习", Status: entity.SceneStatusPending, Brief: "练习题尚未生成"},
		{ClassroomID: 1, SortOrder: 3, Title: "课程结束", Type: entity.SceneTypeComplete, Status: entity.SceneStatusReady},
	}
}

func TestSelectLessonSceneUsesExplicitPageBeforeCurrentPage(t *testing.T) {
	scenes := lessonScenes()
	selected, err := selectLessonScene(scenes, scenes[1].ID, "第一页讲的是什么？")
	if err != nil || selected == nil || selected.Title != "认识 Agent" {
		t.Fatalf("明确问第一页时选中 %#v, %v", selected, err)
	}
}

func TestSelectLessonSceneRejectsForeignPage(t *testing.T) {
	scenes := lessonScenes()
	if _, err := selectLessonScene(scenes, 99, "当前页讲什么"); err == nil {
		t.Fatal("其他课堂的页面 ID 不应被接受")
	}
}

func TestFormatLessonMaterialIncludesContentAndUnreadyState(t *testing.T) {
	scenes := lessonScenes()
	material := formatLessonMaterial(scenes, &scenes[0], []entity.SceneSegment{{Text: "讲解稿强调了感知、决策和行动。", Status: entity.SceneSegmentStatusReady}})
	for _, part := range []string{"第1页", "认识 Agent", "Agent 能感知环境并采取行动", "讲解稿强调了感知、决策和行动", "工具调用后要观察结果", "待生成练习", "尚未生成完成"} {
		if !strings.Contains(material, part) {
			t.Errorf("课件参考缺少 %q: %s", part, material)
		}
	}
	if strings.Contains(material, "课程结束") {
		t.Error("课程结束页不应当作内容页")
	}
}

func TestFormatLessonMaterialDoesNotInventPendingContent(t *testing.T) {
	scenes := lessonScenes()
	material := formatLessonMaterial(scenes, &scenes[2], nil)
	if !strings.Contains(material, "第3页") || !strings.Contains(material, "尚未生成完成") {
		t.Fatalf("未就绪页应明确说明状态: %s", material)
	}
	if strings.Contains(material, "练习题尚未生成") {
		t.Error("未就绪页的大纲草稿不能冒充已生成正文")
	}
}

func TestFormatLessonMaterialKeepsSelectedLatePage(t *testing.T) {
	scenes := make([]entity.Scene, 30)
	for i := range scenes {
		scenes[i] = entity.Scene{
			BaseModel: entity.BaseModel{ID: uint64(i + 1)},
			Title:     fmt.Sprintf("第%d个主题", i+1),
			Status:    entity.SceneStatusReady,
			Content:   json.RawMessage(fmt.Sprintf(`{"blocks":[{"type":"paragraph","content":"%s"}]}`, strings.Repeat("旧页面内容", 120))),
		}
	}
	scenes[29].Content = json.RawMessage(`{"blocks":[{"type":"paragraph","content":"第三十页的独有结论是先观察后行动。"}]}`)
	material := formatLessonMaterial(scenes, &scenes[29], nil)
	if !strings.Contains(material, "第三十页的独有结论是先观察后行动") {
		t.Fatal("后面的选中页被前面页面挤出上下文")
	}
}

func TestSceneTextIncludesQuizAnswer(t *testing.T) {
	scene := entity.Scene{Content: json.RawMessage(`{"blocks":[{"type":"quiz","content":"Agent 的闭环包含什么？","interaction":{"options":["观察反馈并调整","只执行固定脚本"],"answer":"观察反馈并调整","config":{"explanation":"反馈使行动可调整"}}}]}`)}
	text := sceneText(scene)
	for _, part := range []string{"Agent 的闭环包含什么", "只执行固定脚本", "答案：观察反馈并调整", "反馈使行动可调整"} {
		if !strings.Contains(text, part) {
			t.Errorf("测验正文缺少 %q: %s", part, text)
		}
	}
}

func TestSceneTextIncludesInteractiveVisibleContentOnly(t *testing.T) {
	scene := entity.Scene{
		Content:         json.RawMessage(`{"blocks":[{"type":"paragraph","content":"调节温度"}]}`),
		InteractiveHTML: `<html><style>.hidden{color:red}</style><body><h1>恒温器反馈实验</h1><button>升高温度</button><script>secretInstruction()</script></body></html>`,
	}
	text := sceneText(scene)
	for _, part := range []string{"调节温度", "恒温器反馈实验", "升高温度"} {
		if !strings.Contains(text, part) {
			t.Errorf("交互页缺少可见内容 %q: %s", part, text)
		}
	}
	for _, part := range []string{"secretInstruction", "color:red"} {
		if strings.Contains(text, part) {
			t.Errorf("交互页不应引用脚本或样式 %q: %s", part, text)
		}
	}
}
