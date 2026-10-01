package classroom

import (
	"fmt"
	"strings"

	"narra/internal/model/entity"
)

// maxOutlineSummaryRunes 是页目录里一句话摘要的长度上限。
const maxOutlineSummaryRunes = 40

// ClassroomContext 是分层上下文里的课堂级信息，只保留课程定位。
type ClassroomContext struct {
	Requirement        string
	Mode               string
	LearningObjectives []string
	Audience           string
}

// OutlineEntry 是分层上下文里的一页索引项，只够让模型知道这门课还有哪些页。
type OutlineEntry struct {
	PlanID  string
	Order   int
	Type    string
	Title   string
	Summary string
}

// NeighborContext 是当前页前后各一页的索引，供衔接语使用。
type NeighborContext struct {
	Previous *OutlineEntry
	Next     *OutlineEntry
}

// PageContext 是生成一页所需的全部上下文，按层组织而不是每页重发整份计划。
type PageContext struct {
	Classroom ClassroomContext
	Outline   []OutlineEntry
	Current   PlanPage
	Neighbor  NeighborContext
	Teacher   entity.PresetAgent
}

// writePrompt 把课堂、页目录、邻页与当前页写进提示词，供内容与讲稿两处复用。
func (p PageContext) writePrompt(builder *strings.Builder) {
	p.writeClassroom(builder)
	p.writeOutline(builder)
	p.writeNeighbors(builder)
	p.writeCurrent(builder)
}

func (p PageContext) writeExecutionPrompt(builder *strings.Builder) {
	p.writeClassroom(builder)
	p.writeNeighbors(builder)
	p.writeCurrent(builder)
}

func (p PageContext) writeFocusedPrompt(builder *strings.Builder) {
	p.writeClassroom(builder)
	p.writeCurrent(builder)
}

// stableClassroomPrompt 是同一课堂所有页面共享的消息前缀。独立成一条消息后，页面差异不会
// 改变它的字节内容，支持前缀缓存的模型服务可以跨页复用 system prompt + 这段课堂信息。
func (p PageContext) stableClassroomPrompt() string {
	var builder strings.Builder
	p.writeClassroom(&builder)
	return builder.String()
}

func (p PageContext) executionPagePrompt() string {
	var builder strings.Builder
	p.writeNeighbors(&builder)
	p.writeCurrent(&builder)
	return builder.String()
}

func (p PageContext) focusedPagePrompt() string {
	var builder strings.Builder
	p.writeCurrent(&builder)
	return builder.String()
}

func (p PageContext) writeClassroom(builder *strings.Builder) {
	builder.WriteString("## 课堂\n")
	fmt.Fprintf(builder, "需求：%s\n模式：%s\n", p.Classroom.Requirement, p.Classroom.Mode)
	if len(p.Classroom.LearningObjectives) > 0 {
		fmt.Fprintf(builder, "学完这门课能做到：%s\n", strings.Join(p.Classroom.LearningObjectives, "；"))
	}
	if p.Classroom.Audience != "" {
		fmt.Fprintf(builder, "学员情况：%s\n", p.Classroom.Audience)
	}
}

func (p PageContext) writeOutline(builder *strings.Builder) {
	builder.WriteString("\n## 课程页目录\n")
	for _, entry := range p.Outline {
		fmt.Fprintf(builder, "%d. [%s] %s：%s\n", entry.Order+1, entry.Type, entry.Title, entry.Summary)
	}
}

func (p PageContext) writeNeighbors(builder *strings.Builder) {
	if p.Neighbor.Previous != nil {
		fmt.Fprintf(builder, "\n## 上一页\n[%s] %s：%s\n",
			p.Neighbor.Previous.Type, p.Neighbor.Previous.Title, p.Neighbor.Previous.Summary)
	}
	if p.Neighbor.Next != nil {
		fmt.Fprintf(builder, "\n## 下一页\n[%s] %s：%s\n",
			p.Neighbor.Next.Type, p.Neighbor.Next.Title, p.Neighbor.Next.Summary)
	}
}

func (p PageContext) writeCurrent(builder *strings.Builder) {
	fmt.Fprintf(builder, "\n## 当前页\n第 %d 页，类型 %s，标题《%s》\n要讲清：%s\n",
		p.Current.Order+1, p.Current.Type, p.Current.Title, p.Current.Brief)
	if p.Current.LearningObjective != "" {
		fmt.Fprintf(builder, "学完这一页能做到：%s\n", p.Current.LearningObjective)
	}
	if len(p.Current.NarrationFocus) > 0 {
		fmt.Fprintf(builder, "讲解重点：%s\n", strings.Join(p.Current.NarrationFocus, "；"))
	}
}

// writeRevision 把上一轮的问题写进提示词，没有问题时什么都不写。
func writeRevision(builder *strings.Builder, revision, feedback string) {
	if strings.TrimSpace(revision) != "" {
		fmt.Fprintf(builder, "\n## 审核意见（必须逐条落实）\n%s\n", strings.TrimSpace(revision))
	}
	if strings.TrimSpace(feedback) != "" {
		fmt.Fprintf(builder, "\n## 上一次输出没有通过校验\n%s\n请修正后重新输出完整结果，不要解释。\n", strings.TrimSpace(feedback))
	}
}

// writeContentRequirements 把这一页「必须包含」的内容要点写进提示词。
//
// 这份清单由页规划产出，此前只有审核看得见：审核拿着它逐条比对、逐条扣分，
// 而写内容的人从没见过，等于在盲写。写在这里是让交付方在动手前就知道边界。
func writeContentRequirements(builder *strings.Builder, plan *PageExecutionPlan) {
	if plan == nil || len(plan.ContentRequirements) == 0 {
		return
	}
	builder.WriteString("\n## 这一页必须包含\n")
	for _, item := range plan.ContentRequirements {
		fmt.Fprintf(builder, "- %s\n", item)
	}
}

// writeAcceptanceCriteria 把这一页的验收条件写进提示词。
//
// 验收条件此前只出现在审核的提示词里，是审核逐条打回的依据；交给交付方，
// 是让它交付前先照单自查一遍——缺一项就是一次白跑的打回与修订。
func writeAcceptanceCriteria(builder *strings.Builder, plan *PageExecutionPlan) {
	if plan == nil || len(plan.AcceptanceCriteria) == 0 {
		return
	}
	builder.WriteString("\n## 本页验收条件（交付前逐条自查，少一条都不算完成）\n")
	for _, item := range plan.AcceptanceCriteria {
		fmt.Fprintf(builder, "- %s\n", item)
	}
}

// writeNarrationReference 把上一版讲稿写进内容专家的提示词，只在修订时用得上。
//
// 审核反复指出「这个要点只讲在讲稿里、页面上没有」——而内容专家修订时看不到讲稿，
// 只能靠审核意见的转述去猜要补什么。把讲稿原文给它，这类问题才改得动。
func writeNarrationReference(builder *strings.Builder, items []narrationSegment) {
	written := false
	for _, item := range items {
		if strings.TrimSpace(item.Text) == "" {
			continue
		}
		if !written {
			builder.WriteString("\n## 上一版讲稿\n审核指出下列要点只讲在讲稿里、页面上没有。请把它们补进内容块；页面已经覆盖的不必重复。\n")
			written = true
		}
		fmt.Fprintf(builder, "- %s\n", strings.TrimSpace(item.Text))
	}
}

// buildClassroomContext 组装课堂级上下文；受众把等级与背景并成一句。
func buildClassroomContext(classroom *entity.Classroom, plan *ClassroomPlan) ClassroomContext {
	audience := strings.TrimSpace(plan.Audience.Level)
	if background := strings.TrimSpace(plan.Audience.Background); background != "" {
		if audience != "" {
			audience += "："
		}
		audience += background
	}
	return ClassroomContext{
		Requirement:        classroom.Requirement,
		Mode:               classroom.Mode,
		LearningObjectives: plan.LearningObjectives,
		Audience:           audience,
	}
}

// outlineIndex 把计划里的页压成目录索引，每页只留一句话摘要。
func outlineIndex(pages []PlanPage) []OutlineEntry {
	entries := make([]OutlineEntry, 0, len(pages))
	for _, page := range pages {
		entries = append(entries, outlineEntry(page))
	}
	return entries
}

// outlineEntry 把一页压成索引项。
func outlineEntry(page PlanPage) OutlineEntry {
	return OutlineEntry{
		PlanID:  page.PlanID,
		Order:   page.Order,
		Type:    page.Type,
		Title:   page.Title,
		Summary: firstSentence(page.Brief, maxOutlineSummaryRunes),
	}
}

// neighborOf 取页序上紧邻的前后两页索引。
func neighborOf(entries []OutlineEntry, order int) NeighborContext {
	var neighbor NeighborContext
	for index := range entries {
		previous := entries[index]
		switch previous.Order {
		case order - 1:
			neighbor.Previous = &previous
		case order + 1:
			neighbor.Next = &previous
		}
	}
	return neighbor
}

// firstSentence 取文本的第一个句子，超出上限按字符截断。
func firstSentence(text string, max int) string {
	trimmed := strings.TrimSpace(text)
	if index := strings.IndexAny(trimmed, "。；！？\n"); index >= 0 {
		trimmed = trimmed[:index]
	}
	return truncateRunes(trimmed, max)
}
