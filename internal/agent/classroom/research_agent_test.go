package classroom

import (
	"strings"
	"testing"

	"narra/internal/material"
)

// 钉住"每页调研怎么用本课材料"：摘录进提示词、带来源与章节；没有摘录时输入不变。

func TestResearchPromptIncludesMaterialExcerpts(t *testing.T) {
	page := PageContext{Current: PlanPage{Order: 0, Type: "slide", Title: "令牌桶", Brief: "讲清限流思路"}}
	in := &researchInput{
		Page: page,
		MaterialExcerpts: []material.Hit{{
			Source:      "讲义.md",
			SectionPath: "第一章 限流",
			Content:     "令牌桶按固定速率放令牌",
		}},
	}
	prompt := researchPrompt(in)
	for _, want := range []string{"本课材料摘录", "讲义.md · 第一章 限流", "令牌桶按固定速率放令牌"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("调研输入缺少 %q:\n%s", want, prompt)
		}
	}

	empty := researchPrompt(&researchInput{Page: page})
	if strings.Contains(empty, "本课材料摘录") {
		t.Fatalf("没有摘录时不该出现材料节:\n%s", empty)
	}
}

func TestMaterialQueryUsesPageFields(t *testing.T) {
	page := PageContext{Current: PlanPage{
		Title:             "令牌桶",
		Brief:             "讲清限流思路",
		LearningObjective: "能说出桶和令牌的作用",
	}}
	query := materialQuery(page)
	for _, want := range []string{"令牌桶", "讲清限流思路", "能说出桶和令牌的作用"} {
		if !strings.Contains(query, want) {
			t.Fatalf("检索词缺少 %q: %q", want, query)
		}
	}
	if got := materialQuery(PageContext{}); got != "" {
		t.Fatalf("空页面应拼出空检索词: %q", got)
	}
}
