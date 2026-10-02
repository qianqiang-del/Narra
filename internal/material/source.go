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
// 预算按材料顺序先到先得：先小材料全文，再大材料纲要，最后（还有预算时）大材料的
// 需求相关节选。任何一份材料读不到都只记日志跳过，不影响其余材料。
func (s *source) Snapshot(ctx context.Context, refs []Ref, query string) []Block {
	if len(refs) == 0 {
		return nil
	}
	blocks := make([]Block, 0, len(refs))
	remaining := PlannerBudgetChars
	var bigDocumentIDs []uint64

	for _, ref := range refs {
		if remaining <= 0 {
			break
		}
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

		if runeLen(document.Content) <= SmallDocChars {
			text, truncated := fitBudget(document.Content, remaining)
			remaining -= runeLen(text)
			blocks = append(blocks, Block{DocumentID: ref.DocumentID, Name: name, Text: text, Truncated: truncated})
			continue
		}

		bigDocumentIDs = append(bigDocumentIDs, ref.DocumentID)
		chunks, err := s.chunks.ListChunksByDocument(ctx, ref.DocumentID)
		if err != nil {
			logger.Warn("读取课程材料切片失败，跳过",
				zap.Uint64("document_id", ref.DocumentID), zap.Error(err))
			continue
		}
		outline, truncated := fitBudget(buildOutline(chunks), remaining)
		if strings.TrimSpace(outline) == "" {
			continue
		}
		remaining -= runeLen(outline)
		blocks = append(blocks, Block{DocumentID: ref.DocumentID, Name: name, Text: outline, Truncated: truncated})
	}

	// 大材料再补一段"需求相关节选"：纲要说"材料里有什么"，节选把"这次要用的"递到手里。
	if remaining > 0 && len(bigDocumentIDs) > 0 {
		if query = strings.TrimSpace(query); query != "" {
			hits, err := s.Retrieve(ctx, bigDocumentIDs, query, snapshotTopK)
			if err != nil {
				logger.Warn("课程材料定向检索失败，只用纲要", zap.Error(err))
			} else if text := formatHits(hits, remaining); text != "" {
				blocks = append(blocks, Block{Name: "材料节选", Text: text})
			}
		}
	}
	return blocks
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

// buildOutline 从切片生成"材料纲要"。
//
// 有章节身份的按节归并（标题之前的内容归到「（前言）」）；整篇都没有章节的，
// 按 chunk_index 顺序每 windowChunks 片切一个"伪章节"。两种形态都保持原文顺序，
// 字数写进纲要用给模型判断内容多少。
func buildOutline(chunks []entity.KnowledgeChunk) string {
	if len(chunks) == 0 {
		return ""
	}
	for _, chunk := range chunks {
		if sectionPathOf(chunk) != "" {
			return outlineBySection(chunks)
		}
	}
	return outlineByWindow(chunks)
}

type outlineSection struct {
	name    string
	chars   int
	preview string
}

func sectionPathOf(chunk entity.KnowledgeChunk) string {
	if chunk.SectionPath == nil {
		return ""
	}
	return strings.TrimSpace(*chunk.SectionPath)
}

func outlineBySection(chunks []entity.KnowledgeChunk) string {
	sections := make([]outlineSection, 0, 16)
	positionByName := make(map[string]int)
	for _, chunk := range chunks {
		name := sectionPathOf(chunk)
		if name == "" {
			name = "（前言）"
		}
		position, ok := positionByName[name]
		if !ok {
			sections = append(sections, outlineSection{name: name, preview: previewOf(chunk.Content)})
			position = len(sections) - 1
			positionByName[name] = position
		}
		sections[position].chars += runeLen(chunk.Content)
	}
	return renderOutline(sections)
}

func outlineByWindow(chunks []entity.KnowledgeChunk) string {
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
		}
		sections = append(sections, section)
	}
	return renderOutline(sections)
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
