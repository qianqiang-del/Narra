// Package einoretriever 把项目自己的 rag.Retriever 接进 Eino 的检索生态。
//
// 两个出口：
//   - Retriever：实现 Eino 的 retriever.Retriever（query → []*schema.Document），
//     供 Graph 编排或 eino-ext 的流程（multiquery / router / parent）使用；
//   - MultiQuery：实现与 rag.Retriever 相同的方法签名，把输入里的 Variants 展开成
//     多路检索，经 Eino 的 multiquery 流程并发召回后按 RRF 融合（见 multi.go）。
//
// 为什么需要适配器：Eino 官方没有 pgvector 实现（eino-ext 覆盖 Milvus / ES /
// OpenSearch / Qdrant / Redis 等），要接它的接口与流程就只能自己实现这一个方法。
// 适配器保持薄：清洗、两条召回路的融合都在 rag 里，这里只做映射与编排。
package einoretriever

import (
	"context"
	"fmt"
	"strconv"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"

	"narra/internal/model/entity"
	"narra/internal/rag"
)

// Searcher 是本包对检索能力的最小依赖面；*rag.Retriever 满足它。
// 收窄成接口是为了测试能注入替身（与 internal/mcp/knowledge_tool.go 同一个路数）。
type Searcher interface {
	Retrieve(ctx context.Context, input rag.RetrieveInput) (rag.RetrieveResult, error)
}

// Document.MetaData 的键。它们携带项目侧的检索字段：多查询融合从这里读，
// 将来接 Graph 的下游也可以从这里取，不必再包一层结构体。
const (
	metaDocumentID  = "document_id"
	metaChunkIndex  = "chunk_index"
	metaTitle       = "title"
	metaHeading     = "heading"
	metaSectionPath = "section_path" // 节路径；装配层按它折叠与拼整节
	metaSymbol      = "symbol"       // 代码符号；装配层按它折叠与拼整符号
	metaSourceType  = "source_type"
	metaSource      = "source"     // 对外展示用的来源（SourceURI 为空时回落到标题）
	metaSourceURI   = "source_uri" // 原始来源标识；手工录入时为空，用于还原 Hit
	metaMethod      = "method"
	metaSimilarity  = "similarity"
	metaModel       = "model"
	metaTerms       = "terms"
)

// Retriever 把 rag.Retriever 适配成 Eino 的 retriever.Retriever。
type Retriever struct {
	inner       Searcher
	defaultTopK int

	// filter 是这次适配器实例携带的文档侧过滤条件。
	//
	// Eino 的 retriever.Retriever 接口只有 query 与通用选项，塞不进项目自己的过滤条件；
	// 而过滤又是每次请求的参数，所以多查询门面按调用现建适配器、把条件放进这个字段 ——
	// 与"按调用现建 multiquery"是同一个做法（见 multi.go）。
	filter entity.KnowledgeChunkFilter
}

var (
	_ retriever.Retriever = (*Retriever)(nil)
	_ components.Typer    = (*Retriever)(nil)
	_ components.Checker  = (*Retriever)(nil)
)

// New 创建适配器。defaultTopK 是调用方没通过 WithTopK 指定时的条数，按 rag 的口径钳位。
// 不带过滤条件；需要过滤的调用方走 newRetriever。
func New(inner Searcher, defaultTopK int) (*Retriever, error) {
	return newRetriever(inner, defaultTopK, entity.KnowledgeChunkFilter{})
}

// newRetriever 是带过滤条件的内部构造：每次请求现建一个适配器实例，
// 把这次检索的过滤条件固定在实例上。
func newRetriever(inner Searcher, defaultTopK int, filter entity.KnowledgeChunkFilter) (*Retriever, error) {
	if inner == nil {
		return nil, fmt.Errorf("检索器不能为空")
	}
	return &Retriever{inner: inner, defaultTopK: rag.ClampTopK(defaultTopK), filter: filter}, nil
}

// GetType 是组件显示名（DevOps 工具里显示为 NarraHybridRetriever）。
func (r *Retriever) GetType() string { return "NarraHybrid" }

// IsCallbacksEnabled 返回 true：本实现自己触发带类型的回调，框架不再自动包一层。
func (r *Retriever) IsCallbacksEnabled() bool { return true }

// Retrieve 实现 retriever.Retriever：一次查询召回一批文档。
//
// 项目侧没有语义的通用选项：Index / SubIndex / DSLInfo / Embedding 一律忽略 ——
// 向量模型、文档可用性与词法路都在 rag 内部决定。能用的只有 TopK 与 ScoreThreshold；
// 后者作用在本实现对外暴露的 Score（RRF 融合分）上，与 rag 的口径一致。
func (r *Retriever) Retrieve(ctx context.Context, query string, opts ...retriever.Option) (docs []*schema.Document, err error) {
	options := retriever.GetCommonOptions(&retriever.Options{TopK: &r.defaultTopK}, opts...)
	topK := r.defaultTopK
	if options.TopK != nil {
		topK = rag.ClampTopK(*options.TopK)
	}

	ctx = callbacks.OnStart(ctx, &retriever.CallbackInput{Query: query, TopK: topK})
	defer func() {
		if err != nil {
			callbacks.OnError(ctx, err)
		}
	}()

	result, err := r.inner.Retrieve(ctx, rag.RetrieveInput{Text: query, TopK: topK, Filter: r.filter})
	if err != nil {
		return nil, err
	}

	docs = make([]*schema.Document, 0, len(result.Hits))
	for _, hit := range result.Hits {
		docs = append(docs, hitToDocument(hit, result.Model, result.Terms))
	}
	if options.ScoreThreshold != nil {
		docs = filterByScore(docs, *options.ScoreThreshold)
	}

	callbacks.OnEnd(ctx, &retriever.CallbackOutput{Docs: docs})
	return docs, nil
}

// hitToDocument 把一条命中翻成 Eino 的 Document。
//
// ID 用**切片 ID**：multiquery 的默认融合按 Document.ID 去重，将来任何按 ID 合并的
// 下游也认它 —— 用文档 ID 会把同一篇文档的多个切片合并掉，切片级排序就没了。
func hitToDocument(hit rag.Hit, model string, terms []string) *schema.Document {
	displaySource := hit.SourceURI
	if displaySource == "" {
		displaySource = hit.DocumentTitle
	}
	metadata := map[string]any{
		metaDocumentID:  hit.DocumentID,
		metaChunkIndex:  hit.ChunkIndex,
		metaTitle:       hit.DocumentTitle,
		metaHeading:     hit.Heading,
		metaSectionPath: hit.SectionPath,
		metaSymbol:      hit.Symbol,
		metaSourceType:  hit.SourceType,
		metaSource:      displaySource,
		metaSourceURI:   hit.SourceURI,
		metaMethod:      hit.Method,
		metaModel:       model,
		metaTerms:       terms,
	}
	if hit.Similarity != nil {
		metadata[metaSimilarity] = *hit.Similarity
	}
	doc := &schema.Document{
		ID:       strconv.FormatUint(hit.ChunkID, 10),
		Content:  hit.Content,
		MetaData: metadata,
	}
	return doc.WithScore(hit.Score)
}

// documentToHit 把一条 Document 还原成项目侧的命中（多查询融合结果转回 rag 结果时用）。
func documentToHit(doc *schema.Document) rag.Hit {
	// ID 由 hitToDocument 生成；解析失败只可能是外部构造的文档，回 0 而不是报错，
	// 让融合后的其他字段仍可用。
	chunkID, _ := strconv.ParseUint(doc.ID, 10, 64)
	hit := rag.Hit{
		ChunkID:       chunkID,
		DocumentID:    metaUint64(doc, metaDocumentID),
		ChunkIndex:    int32(metaInt(doc, metaChunkIndex)),
		Heading:       metaString(doc, metaHeading),
		SectionPath:   metaString(doc, metaSectionPath),
		Symbol:        metaString(doc, metaSymbol),
		Content:       doc.Content,
		DocumentTitle: metaString(doc, metaTitle),
		SourceType:    metaString(doc, metaSourceType),
		SourceURI:     metaString(doc, metaSourceURI),
		Score:         doc.Score(),
		Method:        metaString(doc, metaMethod),
	}
	if methodHasVector(hit.Method) {
		similarity := metaFloat(doc, metaSimilarity)
		hit.Similarity = &similarity
	}
	return hit
}

// filterByScore 按阈值过滤：低于阈值的整条排除（Options.ScoreThreshold 的语义）。
func filterByScore(docs []*schema.Document, threshold float64) []*schema.Document {
	kept := make([]*schema.Document, 0, len(docs))
	for _, doc := range docs {
		if doc.Score() >= threshold {
			kept = append(kept, doc)
		}
	}
	return kept
}

// methodHasVector 判断一个命中来源是否包含向量成分（vector / hybrid）。
func methodHasVector(method string) bool {
	return method == rag.MethodVector || method == rag.MethodHybrid
}

// 下面四个读元数据的助手都容忍缺键与类型不符：Document 可能来自图的上游、
// 也可能来自旧版本序列化，读不到时回零值比 panic 合适。
func metaString(doc *schema.Document, key string) string {
	value, _ := doc.MetaData[key].(string)
	return value
}

func metaUint64(doc *schema.Document, key string) uint64 {
	switch value := doc.MetaData[key].(type) {
	case uint64:
		return value
	case int:
		return uint64(value)
	case float64:
		return uint64(value)
	default:
		return 0
	}
}

func metaInt(doc *schema.Document, key string) int {
	switch value := doc.MetaData[key].(type) {
	case int:
		return value
	case int32:
		return int(value)
	case uint64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func metaFloat(doc *schema.Document, key string) float64 {
	switch value := doc.MetaData[key].(type) {
	case float64:
		return value
	case int:
		return float64(value)
	default:
		return 0
	}
}
