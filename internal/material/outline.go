package material

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"narra/internal/model/entity"
	"narra/pkg/logger"
)

// OutlineVersion 是 material_outline JSON 的结构版本；读到不匹配的版本按未生成处理。
const OutlineVersion = 1

// 摘要生成的预算与闸门。数字按"一份材料 + 常见模型"估，实测后再调。
const (
	// OutlineBuildTimeout 是一堂课为全部材料生成摘要的总时长上限。
	OutlineBuildTimeout = 90 * time.Second
	// outlineBatchSections 是一次模型调用最多总结几节。
	outlineBatchSections = 4
	// outlineMaxSummarizedSections 是一份材料最多给多少节生成摘要；超出的节只留预览。
	outlineMaxSummarizedSections = 40
	// outlineMaxCallsPerDocument 是一份材料的模型调用上限（含文档级摘要）。
	outlineMaxCallsPerDocument = 12
	// outlineMaxCallsPerClassroom 是一堂课全部材料的模型调用上限，超出的材料退回代码目录。
	outlineMaxCallsPerClassroom = 40
	// outlineSectionSampleChars 是送进摘要调用的单节正文上限。
	outlineSectionSampleChars = 2000
	// outlineDocumentSampleChars 是无标题材料送进文档级摘要的正文上限。
	outlineDocumentSampleChars = 3000
	// outlineConcurrency 是同时生成摘要的材料数。
	outlineConcurrency = 3
	// maxSectionSummaryRunes 是单节摘要的长度上限。
	maxSectionSummaryRunes = 120
	// maxOutlineSummaryRunes 是文档级摘要的长度上限。
	maxOutlineSummaryRunes = 200
)

// Outline 是一份材料的目录与摘要，落进 knowledge_documents.material_outline。
type Outline struct {
	Version     int              `json:"version"`
	Checksum    string           `json:"checksum"`
	Model       string           `json:"model"`
	GeneratedAt time.Time        `json:"generated_at"`
	HasHeadings bool             `json:"has_headings"`
	Summary     string           `json:"summary"`
	Sections    []SectionOutline `json:"sections"`
}

// SectionOutline 是目录里的一节；无标题材料里是每 windowChunks 片合成的伪分段。
// Summary 是模型写的一句摘要，可能为空（伪分段、或该节没轮到生成）；空时渲染回退到 Preview。
type SectionOutline struct {
	Path    string `json:"path"`
	Chars   int    `json:"chars"`
	Summary string `json:"summary,omitempty"`
	Preview string `json:"preview,omitempty"`
}

// SectionSample 是送进模型的一节材料节选。
type SectionSample struct {
	Path    string
	Content string
}

// Summarizer 是摘要生成对模型的最小依赖面；由课堂运行时用本课模型实现。
type Summarizer interface {
	// SummarizeSections 为一批章节各写一句摘要，返回与 sections 等长的数组。
	SummarizeSections(ctx context.Context, documentName string, sections []SectionSample) ([]string, error)
	// SummarizeDocument 生成文档级摘要：sections 非空时按章节摘要归并，为空时按正文节选概括。
	SummarizeDocument(ctx context.Context, documentName string, sections []SectionSample) (string, error)
}

// OutlineWriter 写摘要缓存；*repository.KnowledgeDocumentRepository 满足它。
type OutlineWriter interface {
	SaveMaterialOutline(ctx context.Context, id uint64, outline json.RawMessage) error
}

// OutlineBuilder 为一批材料准备目录与摘要：命中缓存直接用，未命中用模型生成并写回。
// 任何失败都表现为"这一份没有摘要"，由 Snapshot 退回代码目录，绝不阻断课堂生成。
type OutlineBuilder interface {
	Build(ctx context.Context, refs []Ref, summarizer Summarizer, modelID string) map[uint64]*Outline
}

// outlineBuilder 是 OutlineBuilder 的默认实现。
type outlineBuilder struct {
	documents DocumentReader
	chunks    ChunkReader
	writer    OutlineWriter
}

// NewOutlineBuilder 构造摘要构建器；三个依赖缺一不可，装配期就报错。
func NewOutlineBuilder(documents DocumentReader, chunks ChunkReader, writer OutlineWriter) (OutlineBuilder, error) {
	if documents == nil || chunks == nil || writer == nil {
		return nil, fmt.Errorf("摘要构建器缺少依赖：documents=%v chunks=%v writer=%v", documents != nil, chunks != nil, writer != nil)
	}
	return &outlineBuilder{documents: documents, chunks: chunks, writer: writer}, nil
}

// Build 见 OutlineBuilder 接口注释。
//
// 材料之间并发（上限 outlineConcurrency），整体受 ctx 截止时间约束；ctx 到点后
// 已完成的材料照常返回，未完成的不写缓存、由调用方退回代码目录。
func (b *outlineBuilder) Build(ctx context.Context, refs []Ref, summarizer Summarizer, modelID string) map[uint64]*Outline {
	outlines := make(map[uint64]*Outline, len(refs))
	if len(refs) == 0 || summarizer == nil {
		return outlines
	}

	var (
		mutex sync.Mutex
		wait  sync.WaitGroup
		calls atomic.Int32
		slots = make(chan struct{}, outlineConcurrency)
	)
	for _, ref := range refs {
		ref := ref
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				return
			}
			outline := b.buildOne(ctx, ref, summarizer, modelID, &calls)
			if outline == nil {
				return
			}
			mutex.Lock()
			outlines[ref.DocumentID] = outline
			mutex.Unlock()
		}()
	}
	wait.Wait()
	logger.Info("材料目录摘要构建完成",
		zap.Int("requested", len(refs)),
		zap.Int("ready", len(outlines)),
		zap.Int32("model_calls", calls.Load()))
	return outlines
}

// buildOne 处理一份材料：缓存 → 生成 → 写回；任何一步失败都返回 nil（调用方走代码兜底）。
func (b *outlineBuilder) buildOne(ctx context.Context, ref Ref, summarizer Summarizer, modelID string, calls *atomic.Int32) *Outline {
	document, err := b.documents.GetByID(ctx, ref.DocumentID)
	if err != nil {
		logger.Warn("读取课程材料失败，跳过摘要生成", zap.Uint64("document_id", ref.DocumentID), zap.Error(err))
		return nil
	}
	if document.Status != entity.KnowledgeDocumentStatusReady || !document.Enabled {
		return nil
	}
	if cached := cachedOutline(document); cached != nil {
		return cached
	}

	chunks, err := b.chunks.ListChunksByDocument(ctx, ref.DocumentID)
	if err != nil {
		logger.Warn("读取课程材料切片失败，跳过摘要生成", zap.Uint64("document_id", ref.DocumentID), zap.Error(err))
		return nil
	}
	sections, hasHeadings := groupOutlineSections(chunks)
	if len(sections) == 0 {
		return nil
	}
	if len(sections) > maxOutlineSections {
		sections = sections[:maxOutlineSections]
	}

	outline := &Outline{
		Version:     OutlineVersion,
		Checksum:    checksumOf(document),
		Model:       modelID,
		GeneratedAt: time.Now().UTC(),
		HasHeadings: hasHeadings,
		Sections:    make([]SectionOutline, 0, len(sections)),
	}
	for _, section := range sections {
		outline.Sections = append(outline.Sections, SectionOutline{
			Path:    section.name,
			Chars:   section.chars,
			Preview: section.preview,
		})
	}

	var documentCalls int
	summarized := b.summarizeSections(ctx, document.Title, sections, outline, summarizer, calls, &documentCalls)
	b.summarizeDocument(ctx, document.Title, document.Content, outline, summarizer, calls, &documentCalls)

	// 一条模型摘要都没拿到：不写缓存，让 Snapshot 用代码目录（下次开课还会重试）。
	if !summarized && strings.TrimSpace(outline.Summary) == "" {
		return nil
	}

	raw, err := json.Marshal(outline)
	if err != nil {
		logger.Warn("编码材料摘要失败", zap.Uint64("document_id", ref.DocumentID), zap.Error(err))
		return outline
	}
	if err := b.writer.SaveMaterialOutline(ctx, ref.DocumentID, raw); err != nil {
		logger.Warn("写入材料摘要失败，本次仍用内存结果", zap.Uint64("document_id", ref.DocumentID), zap.Error(err))
	}
	return outline
}

// summarizeSections 分批给章节生成摘要；返回是否至少拿到一条。
// 无标题材料只有伪分段，不做章节摘要，直接交给文档级摘要。
func (b *outlineBuilder) summarizeSections(
	ctx context.Context,
	documentName string,
	sections []outlineSection,
	outline *Outline,
	summarizer Summarizer,
	calls *atomic.Int32,
	documentCalls *int,
) bool {
	if !outline.HasHeadings || len(outline.Sections) == 0 {
		return false
	}

	indexes := make([]int, len(outline.Sections))
	for index := range indexes {
		indexes[index] = index
	}
	if len(indexes) > outlineMaxSummarizedSections {
		if top := topLevelSectionIndexes(outline.Sections); len(top) > 0 && len(top) <= outlineMaxSummarizedSections {
			indexes = top
		} else {
			indexes = indexes[:outlineMaxSummarizedSections]
		}
	}

	got := false
	for start := 0; start < len(indexes); start += outlineBatchSections {
		if ctx.Err() != nil || !canSpend(calls, documentCalls) {
			break
		}
		end := start + outlineBatchSections
		if end > len(indexes) {
			end = len(indexes)
		}
		batch := indexes[start:end]

		samples := make([]SectionSample, 0, len(batch))
		for _, index := range batch {
			samples = append(samples, SectionSample{
				Path:    outline.Sections[index].Path,
				Content: truncateRunes(sections[index].content, outlineSectionSampleChars),
			})
		}
		summaries, err := summarizer.SummarizeSections(ctx, documentName, samples)
		if err != nil {
			logger.Warn("生成材料章节摘要失败，其余章节退回预览",
				zap.String("document", documentName), zap.Error(err))
			break
		}
		if len(summaries) != len(batch) {
			logger.Warn("章节摘要条数与请求不一致，丢弃这一批",
				zap.String("document", documentName), zap.Int("want", len(batch)), zap.Int("got", len(summaries)))
			break
		}
		for offset, index := range batch {
			summary := truncateRunes(strings.TrimSpace(summaries[offset]), maxSectionSummaryRunes)
			if summary == "" {
				continue
			}
			outline.Sections[index].Summary = summary
			got = true
		}
	}
	return got
}

// summarizeDocument 生成文档级摘要：有章节摘要时按它们归并，否则用正文节选概括。
func (b *outlineBuilder) summarizeDocument(
	ctx context.Context,
	documentName string,
	content string,
	outline *Outline,
	summarizer Summarizer,
	calls *atomic.Int32,
	documentCalls *int,
) {
	if ctx.Err() != nil || !canSpend(calls, documentCalls) {
		return
	}
	var samples []SectionSample
	if outline.HasHeadings {
		for _, section := range outline.Sections {
			if strings.TrimSpace(section.Summary) == "" {
				continue
			}
			samples = append(samples, SectionSample{Path: section.Path, Content: section.Summary})
		}
	}
	if len(samples) == 0 {
		samples = []SectionSample{{Content: truncateRunes(content, outlineDocumentSampleChars)}}
	}
	summary, err := summarizer.SummarizeDocument(ctx, documentName, samples)
	if err != nil {
		logger.Warn("生成材料文档级摘要失败", zap.String("document", documentName), zap.Error(err))
		return
	}
	outline.Summary = truncateRunes(strings.TrimSpace(summary), maxOutlineSummaryRunes)
}

// cachedOutline 读缓存；版本不符或正文校验和对不上都按未生成处理。
func cachedOutline(document *entity.KnowledgeDocument) *Outline {
	if len(document.MaterialOutline) == 0 {
		return nil
	}
	var outline Outline
	if err := json.Unmarshal(document.MaterialOutline, &outline); err != nil {
		return nil
	}
	if outline.Version != OutlineVersion {
		return nil
	}
	if checksum := checksumOf(document); checksum != "" && outline.Checksum != "" && outline.Checksum != checksum {
		return nil
	}
	return &outline
}

// checksumOf 取文档当前的正文校验和；老数据为空时返回空串（按"不校验"处理）。
func checksumOf(document *entity.KnowledgeDocument) string {
	if document.ContentChecksum == nil {
		return ""
	}
	return strings.TrimSpace(*document.ContentChecksum)
}

// topLevelSectionIndexes 取一级章节的下标；路径不含 "/" 的算一级。
func topLevelSectionIndexes(sections []SectionOutline) []int {
	indexes := make([]int, 0, len(sections))
	for index, section := range sections {
		if !strings.Contains(section.Path, "/") {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

// canSpend 记一次模型调用；单份或整课的额度用完时返回 false。
func canSpend(calls *atomic.Int32, documentCalls *int) bool {
	if *documentCalls >= outlineMaxCallsPerDocument || calls.Load() >= outlineMaxCallsPerClassroom {
		return false
	}
	calls.Add(1)
	*documentCalls++
	return true
}
