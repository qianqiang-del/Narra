// Package material 是课程材料在生成侧的消费运行时。
//
// 它把 classrooms.generation_config 里的材料引用（document_id）变成模型能读到的文本：
//   - Snapshot：规划阶段用。预算分两层：摘要层给每份材料保底（文档摘要 + 章节摘要，
//     无标题材料是伪分段 + 文档摘要），正文层用剩余预算给小材料全文、给大材料
//     "需求相关节选"；由代码在固定的时机调用，不经过模型选不选工具；
//   - Retrieve：页面阶段用。只在本课材料的切片里按页内容检索，作为调研证据的补充。
//
// 本包只依赖窄接口（由 repository / service 满足），不 import 服务层实现；
// 失败一律"记日志、跳过"，材料是加法，绝不阻断课堂生成。
package material

import (
	"context"
	"fmt"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
)

// 预算口径（按 rune 计）。V1 先写成常量：数字按"一份材料 + 常见模型上下文"估，
// 实测后再决定要不要挪进配置。
const (
	// SmallDocChars 单篇正文不超过它时，规划阶段直接注入全文。
	SmallDocChars = 6000
	// PlannerBudgetChars 是规划阶段所有材料文本的总预算。
	PlannerBudgetChars = 8000
	// SummaryLayerBudgetChars 是摘要层的总盘子：先从总预算里划出它，
	// 给每份材料保底一块目录+摘要，保证没有材料会因顺序或大小被挤掉。
	SummaryLayerBudgetChars = 4000
	// PerMaterialOutlineChars 是单份材料摘要层的封顶字数，防止一份大材料霸屏。
	PerMaterialOutlineChars = 800
	// PageTopK 是每页调研从本课材料里召回的条数。
	PageTopK = 5
	// snapshotTopK 是规划阶段"需求相关节选"的召回条数。
	snapshotTopK = 8
	// outlinePreviewRunes 是纲要里每节的预览字数。
	outlinePreviewRunes = 60
	// windowChunks 是无结构文档按每多少片切一个"伪章节"。
	windowChunks = 5
	// maxOutlineSections 是纲要最多列多少节，防止碎结构把预算刷爆。
	maxOutlineSections = 60
)

// Ref 是建课时写进 generation_config 的材料引用快照。
type Ref struct {
	DocumentID uint64 `json:"document_id"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
}

// Block 是一份材料给规划阶段用的文本块。
type Block struct {
	DocumentID uint64
	Name       string
	Text       string
	// Truncated 表示 Text 因预算被截断，调用方可据此向用户交代"材料只带了节选"。
	Truncated bool
}

// Hit 是材料范围内的一条检索命中。
type Hit struct {
	DocumentID  uint64
	Source      string
	SectionPath string
	Content     string
	Score       float64
}

// Source 是生成侧消费课程材料的最小依赖面。
type Source interface {
	// Snapshot 返回规划用的材料文本。永不报错：单份材料读不到就跳过并记日志。
	// query 是用户需求，用来为大材料补充"需求相关节选"；为空时只给纲要。
	// outlines 是调用方预生成的材料目录+摘要（可为空）；缺失的材料退回代码目录。
	Snapshot(ctx context.Context, refs []Ref, query string, outlines map[uint64]*Outline) []Block

	// Retrieve 只在这些文档的切片里按 query 召回。documentIDs 为空时返回空。
	Retrieve(ctx context.Context, documentIDs []uint64, query string, topK int) ([]Hit, error)
}

// Retriever 是知识库检索能力；*service.KnowledgeService 满足它。
type Retriever interface {
	Retrieve(ctx context.Context, input requestdto.KnowledgeRetrieve) (responsedto.KnowledgeRetrieveResult, error)
}

// DocumentReader 按 ID 取知识文档（正文与状态）；*repository.KnowledgeDocumentRepository 满足它。
type DocumentReader interface {
	GetByID(ctx context.Context, id uint64) (*entity.KnowledgeDocument, error)
}

// ChunkReader 按文档取全部切片（chunk_index 升序）；*repository.KnowledgeDocumentRepository 满足它。
type ChunkReader interface {
	ListChunksByDocument(ctx context.Context, id uint64) ([]entity.KnowledgeChunk, error)
}

// source 是 Source 的默认实现。
type source struct {
	documents DocumentReader
	chunks    ChunkReader
	retriever Retriever
}

// NewSource 构造材料消费运行时。三个依赖缺一不可，装配期就报错。
func NewSource(documents DocumentReader, chunks ChunkReader, retriever Retriever) (Source, error) {
	if documents == nil || chunks == nil || retriever == nil {
		return nil, fmt.Errorf("材料消费组件缺少依赖：documents=%v chunks=%v retriever=%v", documents != nil, chunks != nil, retriever != nil)
	}
	return &source{documents: documents, chunks: chunks, retriever: retriever}, nil
}
