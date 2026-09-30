package repository

import (
	"context"

	"narra/internal/model/entity"
)

// KnowledgeSearchRepository 是检索侧的持久化：只读地召回候选切片。
//
// 与 KnowledgeDocumentRepository 分成两个文件，是因为读者完全不同：
// 那边是收录链路与文档列表（写多、按行操作），这边是检索（只读、按相似度/词项召回）。
// 两条召回路各一个方法，形状由 entity.KnowledgeChunkView / KnowledgeVectorQuery /
// KnowledgeLexicalQuery 定死，internal/rag 靠它实现自己的窄接口 rag.ChunkSearcher。
//
// 两条路都**只召回可检索的切片**：所属文档必须是 ready 且 enabled ——
// 还在处理中的文档切片是半成品，被停用的文档用户已经说了不参与检索
// （见 knowledge_documents.enabled 的注释）。这条过滤是检索正确性的底线，
// 所以写在 SQL 里而不是留给调用方。
//
// 调用方另给的过滤条件（来源类型、文档集合、时间范围）走 query.Filter，
// 两条路共用同一个构造函数，不许只加在一路：两路搜的范围不一致，
// RRF 融合的"两路都召回"就不再是同一个前提，结果会以很难解释的方式偏移。
type KnowledgeSearchRepository interface {
	// SearchVector 在同一个模型下按余弦距离找最相近的切片，返回按相似度降序的候选。
	//
	// 只比较 query.ModelID 生成的向量（跨模型不可比），比较前两侧都 CAST 成
	// query.Dimensions 维 —— 这个表达式与默认模型的 HNSW 索引定义一致，
	// 改写它就会让索引失效、退回顺序扫描（见 vector_index.go）。
	SearchVector(ctx context.Context, query entity.KnowledgeVectorQuery) ([]entity.KnowledgeChunkView, error)

	// SearchLexical 取命中任意词项的切片，返回按加权命中分降序的候选。
	//
	// 匹配是大小写不敏感的子串匹配（ILIKE），范围是切片正文、章节标题与文档标题
	// 拼起来的那一段；每个词项的权重由调用方给（见 entity.KnowledgeLexicalTerm），
	// 短语只在不改变准入的前提下额外加分。terms 为空时直接返回空结果，不查库。
	SearchLexical(ctx context.Context, query entity.KnowledgeLexicalQuery) ([]entity.KnowledgeChunkView, error)
}
