package material

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	requestdto "narra/internal/model/dto/request"
	"narra/internal/model/entity"
	"narra/pkg/logger"
)

// Snapshot 见 Source 接口注释。
//
// 预算分两层：摘要层先从 SummaryLayerBudgetChars 里给每份材料按份均分
// （单份不超过 PerMaterialOutlineChars），保证没有材料会因顺序或大小被挤掉；
// 正文层再用剩余预算给小材料全文、给大材料收集需求定向检索的名单。
// 任何一份材料读不到都只记日志跳过，不影响其余材料。
func (s *source) Snapshot(ctx context.Context, refs []Ref, query string, outlines map[uint64]*Outline) []Block {
	if len(refs) == 0 {
		return nil
	}

	// 先加载：不可用的材料不占摘要层的份额，顺序仍由调用方给的材料顺序决定。
	loaded := make([]loadedMaterial, 0, len(refs))
	for _, ref := range refs {
		document, err := s.documents.GetByID(ctx, ref.DocumentID)
		if err != nil {
			logger.Warn("读取课程材料失败，跳过", zap.Uint64("document_id", ref.DocumentID), zap.Error(err))
			continue
		}
		if document.Status != entity.KnowledgeDocumentStatusReady || !document.Enabled {
			logger.Warn("课程材料当前不可检索，跳过",
				zap.Uint64("document_id", ref.DocumentID),
				zap.String("status", document.Status),
				zap.Bool("enabled", document.Enabled))
			continue
		}
		name := strings.TrimSpace(ref.Name)
		if name == "" {
			name = document.Title
		}
		loaded = append(loaded, loadedMaterial{ref: ref, document: document, name: name})
	}
	if len(loaded) == 0 {
		return nil
	}

	blocks := make([]Block, 0, len(loaded)+1)
	remaining := PlannerBudgetChars

	// 摘要层：每份材料一块，按份均分、单份封顶。
	share := SummaryLayerBudgetChars / len(loaded)
	if share > PerMaterialOutlineChars {
		share = PerMaterialOutlineChars
	}
	if share < 1 {
		share = 1
	}
	for _, item := range loaded {
		if remaining <= 0 {
			break
		}
		budget := share
		if budget > remaining {
			budget = remaining
		}
		text, truncated := s.renderOutlineBlock(ctx, item, outlines[item.ref.DocumentID], budget)
		if strings.TrimSpace(text) == "" {
			continue
		}
		remaining -= runeLen(text)
		blocks = append(blocks, Block{DocumentID: item.ref.DocumentID, Name: item.name, Text: text, Truncated: truncated})
	}

	// 正文层：小材料全文，大材料收集起来做需求定向检索。
	var bigDocumentIDs []uint64
	for _, item := range loaded {
		if remaining <= 0 {
			break
		}
		if runeLen(item.document.Content) > SmallDocChars {
			bigDocumentIDs = append(bigDocumentIDs, item.ref.DocumentID)
			continue
		}
		text, truncated := fitBudget(item.document.Content, remaining)
		remaining -= runeLen(text)
		blocks = append(blocks, Block{DocumentID: item.ref.DocumentID, Name: item.name, Text: text, Truncated: truncated})
	}

	// 大材料再补一段"需求相关节选"：目录说"材料里有什么"，节选把"这次要用的"递到手里。
	if remaining > 0 && len(bigDocumentIDs) > 0 {
		if query = strings.TrimSpace(query); query != "" {
			hits, err := s.Retrieve(ctx, bigDocumentIDs, query, snapshotTopK)
			if err != nil {
				logger.Warn("课程材料定向检索失败，只用目录", zap.Error(err))
			} else if text := formatHits(hits, remaining); text != "" {
				blocks = append(blocks, Block{Name: "材料节选", Text: text})
			}
		}
	}
	return blocks
}

// loadedMaterial 是一次 Snapshot 加载后的材料：引用、文档与展示名。
type loadedMaterial struct {
	ref      Ref
	document *entity.KnowledgeDocument
	name     string
}

// renderOutlineBlock 渲染摘要层的材料块：优先用生成好的目录+摘要，缺失时退回代码目录。
func (s *source) renderOutlineBlock(ctx context.Context, item loadedMaterial, outline *Outline, budget int) (string, bool) {
	if outline != nil {
		if text := renderGeneratedOutline(outline); text != "" {
			return fitBudget(text, budget)
		}
	}
	chunks, err := s.chunks.ListChunksByDocument(ctx, item.ref.DocumentID)
	if err != nil {
		logger.Warn("读取课程材料切片失败，退回正文节选",
			zap.Uint64("document_id", item.ref.DocumentID), zap.Error(err))
		return fitBudget(item.document.Content, budget)
	}
	return fitBudget(buildOutline(chunks), budget)
}

// renderGeneratedOutline 把材料摘要渲染成规划输入里的一节。
// 每行优先用模型写的摘要，没有就回退到代码取的首段预览。
func renderGeneratedOutline(outline *Outline) string {
	var builder strings.Builder
	if summary := strings.TrimSpace(outline.Summary); summary != "" {
		builder.WriteString("文档摘要：")
		builder.WriteString(summary)
		builder.WriteString("\n")
	}
	if len(outline.Sections) == 0 {
		return strings.TrimSpace(builder.String())
	}
	if outline.HasHeadings {
		builder.WriteString("章节：\n")
	} else {
		builder.WriteString("分段：\n")
	}
	for _, section := range outline.Sections {
		fmt.Fprintf(&builder, "- %s（约 %d 字）", section.Path, section.Chars)
		switch {
		case strings.TrimSpace(section.Summary) != "":
			builder.WriteString("：")
			builder.WriteString(strings.TrimSpace(section.Summary))
		case strings.TrimSpace(section.Preview) != "":
			builder.WriteString("：")
			builder.WriteString(strings.TrimSpace(section.Preview))
		}
		builder.WriteString("\n")
	}
	return strings.TrimSpace(builder.String())
}

// Retrieve 见 Source 接口注释。
func (s *source) Retrieve(ctx context.Context, documentIDs []uint64, query string, topK int) ([]Hit, error) {
	query = strings.TrimSpace(query)
	if len(documentIDs) == 0 || query == "" {
		return nil, nil
	}
	result, err := s.retriever.Retrieve(ctx, requestdto.KnowledgeRetrieve{
		Query:   query,
		TopK:    topK,
		Filters: &requestdto.KnowledgeRetrieveFilters{DocumentIDs: documentIDs},
	})
	if err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(result.Results))
	for _, item := range result.Results {
		content := strings.TrimSpace(item.Context)
		if content == "" {
			content = item.Content
		}
		hits = append(hits, Hit{
			DocumentID:  item.DocumentID,
			Source:      item.Source,
			SectionPath: item.SectionPath,
			Content:     content,
			Score:       item.Score,
		})
	}
	return hits, nil
}

// groupOutlineSections 把切片归并成目录项：有章节身份按节，整篇都没有就按窗口切伪分段。
// 第二个返回值表示是否用了真实章节结构，渲染与摘要生成据此区分「章节 / 分段」。
// 两种形态都保持原文顺序；content 只在生成摘要时用（截断后送模型），不落缓存。
func groupOutlineSections(chunks []entity.KnowledgeChunk) ([]outlineSection, bool) {
	if len(chunks) == 0 {
		return nil, false
	}
	for _, chunk := range chunks {
		if sectionPathOf(chunk) != "" {
			return outlineBySection(chunks), true
		}
	}
	return outlineByWindow(chunks), false
}

// buildOutline 从切片生成"材料纲要"文本，供没有生成摘要的材料兜底。
func buildOutline(chunks []entity.KnowledgeChunk) string {
	sections, _ := groupOutlineSections(chunks)
	return renderOutline(sections)
}

type outlineSection struct {
	name    string
	chars   int
	preview string
	content string
}

func sectionPathOf(chunk entity.KnowledgeChunk) string {
	if chunk.SectionPath == nil {
		return ""
	}
	return strings.TrimSpace(*chunk.SectionPath)
}

func outlineBySection(chunks []entity.KnowledgeChunk) []outlineSection {
	sections := make([]outlineSection, 0, 16)
	positionByName := make(map[string]int)
	for _, chunk := range chunks {
		name := sectionPathOf(chunk)
		if name == "" {
			name = "（前言）"
		}
		position, ok := positionByName[name]
		if !ok {
			sections = append(sections, outlineSection{
				name:    name,
				preview: previewOf(chunk.Content),
				content: chunk.Content,
			})
			position = len(sections) - 1
			positionByName[name] = position
		} else {
			sections[position].content += "\n" + chunk.Content
		}
		sections[position].chars += runeLen(chunk.Content)
	}
	return sections
}

func outlineByWindow(chunks []entity.KnowledgeChunk) []outlineSection {
	sections := make([]outlineSection, 0, len(chunks)/windowChunks+1)
	for start := 0; start < len(chunks); start += windowChunks {
		end := start + windowChunks
		if end > len(chunks) {
			end = len(chunks)
		}
		section := outlineSection{
			name:    fmt.Sprintf("第 %d 段", len(sections)+1),
			preview: previewOf(chunks[start].Content),
		}
		for _, chunk := range chunks[start:end] {
			section.chars += runeLen(chunk.Content)
			if section.content != "" {
				section.content += "\n"
			}
			section.content += chunk.Content
		}
		sections = append(sections, section)
	}
	return sections
}

func renderOutline(sections []outlineSection) string {
	if len(sections) == 0 {
		return ""
	}
	var builder strings.Builder
	limit := len(sections)
	if limit > maxOutlineSections {
		limit = maxOutlineSections
	}
	for _, section := range sections[:limit] {
		fmt.Fprintf(&builder, "- %s（约 %d 字）", section.name, section.chars)
		if section.preview != "" {
			fmt.Fprintf(&builder, "：%s", section.preview)
		}
		builder.WriteString("\n")
	}
	if len(sections) > limit {
		fmt.Fprintf(&builder, "- （后略 %d 节）\n", len(sections)-limit)
	}
	return strings.TrimSpace(builder.String())
}

func previewOf(content string) string {
	preview := strings.Join(strings.Fields(content), " ")
	return truncateRunes(preview, outlinePreviewRunes)
}

// formatHits 把检索命中拼成给模型读的节选文本；按预算整条取舍，不写半条。
func formatHits(hits []Hit, budget int) string {
	if len(hits) == 0 || budget <= 0 {
		return ""
	}
	var builder strings.Builder
	used := 0
	for _, hit := range hits {
		content := strings.TrimSpace(hit.Content)
		if content == "" {
			continue
		}
		label := strings.TrimSpace(hit.Source)
		if hit.SectionPath != "" {
			if label != "" {
				label += " · " + hit.SectionPath
			} else {
				label = hit.SectionPath
			}
		}
		if label == "" {
			label = "材料"
		}
		line := fmt.Sprintf("- 【%s】%s\n", label, content)
		if used+runeLen(line) > budget {
			break
		}
		builder.WriteString(line)
		used += runeLen(line)
	}
	return strings.TrimSpace(builder.String())
}

// fitBudget 按预算裁剪文本，返回裁剪结果与是否发生截断。
func fitBudget(text string, budget int) (string, bool) {
	text = strings.TrimSpace(text)
	if budget <= 0 {
		return "", true
	}
	if runeLen(text) <= budget {
		return text, false
	}
	return strings.TrimSpace(truncateRunes(text, budget)), true
}

func runeLen(text string) int { return len([]rune(text)) }

func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}
