package classroom

import (
	"encoding/json"
	"strings"
	"testing"

	"narra/internal/material"
	"narra/internal/model/entity"
)

// 这组用例钉住规划输入里"本课材料"如何拼装：没有材料时输入不变，
// 有材料时全文/纲要/节选都进消息，且截断会写明。

func TestFormatMaterialSection(t *testing.T) {
	if got := formatMaterialSection(nil); got != "" {
		t.Fatalf("没有材料时不该有这一节: %q", got)
	}
	section := formatMaterialSection([]material.Block{
		{Name: "讲义.md", Text: "正文一"},
		{Name: "书.pdf", Text: "纲要", Truncated: true},
	})
	for _, want := range []string{"## 本课材料", "### 讲义.md", "正文一", "### 书.pdf（节选）", "纲要"} {
		if !strings.Contains(section, want) {
			t.Fatalf("材料节缺少 %q:\n%s", want, section)
		}
	}
}

func TestBuildPlanMessagesIncludesMaterials(t *testing.T) {
	classroom := &entity.Classroom{Requirement: "讲讲 Go 的 GC", Mode: entity.ClassroomModeVocational}
	blocks := []material.Block{{Name: "讲义.md", Text: "三色标记法要点"}}

	messages, err := buildPlanMessages(classroom, GenerationConfig{}, blocks)
	if err != nil {
		t.Fatalf("拼规划消息失败: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("期望 system + user 两条消息，实际 %d", len(messages))
	}
	user := messages[1].Content
	for _, want := range []string{"## 用户需求", "## 本课材料", "讲义.md", "三色标记法要点"} {
		if !strings.Contains(user, want) {
			t.Fatalf("规划输入缺少 %q:\n%s", want, user)
		}
	}

	without, err := buildPlanMessages(classroom, GenerationConfig{}, nil)
	if err != nil {
		t.Fatalf("拼规划消息失败: %v", err)
	}
	if strings.Contains(without[1].Content, "本课材料") {
		t.Fatalf("没有材料时不该出现材料节:\n%s", without[1].Content)
	}
}

func TestGenerationConfigParsesMaterials(t *testing.T) {
	var config GenerationConfig
	raw := `{"llm_provider_id":3,"llm_model_id":"qwen","materials":[{"document_id":7,"name":"讲义.md","size":1024}]}`
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatalf("解析生成配置失败: %v", err)
	}
	if len(config.Materials) != 1 || config.Materials[0].DocumentID != 7 || config.Materials[0].Name != "讲义.md" {
		t.Fatalf("材料引用没有解析出来: %+v", config.Materials)
	}
}
