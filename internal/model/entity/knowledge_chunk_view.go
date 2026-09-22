package entity

// 这里的两个类型都不落库、不建表：它们是检索链路（internal/rag）与检索仓储
// （internal/repository）之间共享的形状。放在 entity 的理由与 ChunkReplacement、
// KnowledgeDocumentQuery 相同 —— 两层都要引用，家就不能偏向任何一层。
// 与 KnowledgeUploadRecordView 同一个路数：JOIN 查询的产物，不是表结构。

// KnowledgeChunkView 是一次检索召回的一行：一个切片，加上把它放回原文所需的定位信息。
//
// 为什么不是 KnowledgeChunk：召回必须连带取出所属文档的标题与来源（界面上要显示
// "这条来自哪篇"），而 KnowledgeChunk 上没有这些列 —— 拿它再逐条查文档就是 N+1，
// 一次检索要多出 top_k 次往返。
type KnowledgeChunkView struct {
	ChunkID        uint64  `gorm:"column:chunk_id"`        // 切片 ID
	DocumentID     uint64  `gorm:"column:document_id"`     // 所属文档 ID
	ChunkIndex     int32   `gorm:"column:chunk_index"`     // 切片在原文中的顺序号，检索后按它还原上下文
	Heading        *string `gorm:"column:heading"`         // 所在章节标题；无章节归属时为 NULL
	Content        string  `gorm:"column:content"`         // 切片正文
	CharacterCount int32   `gorm:"column:character_count"` // 切片字符数
	DocumentTitle  string  `gorm:"column:document_title"`  // 所属文档标题
	SourceType     string  `gorm:"column:source_type"`     // 所属文档的来源类型
	SourceURI      *string `gorm:"column:source_uri"`      // 所属文档的来源标识；手工录入时为 NULL

	// RawScore 是这一行在**所属召回路**里的原始得分：向量路是余弦相似度（越近越大），
	// 词法路是命中的词项数（命中越多越大）。
	//
	// 两条路的口径不在一个量纲上，跨路比较没有意义 —— 融合排序由 rag 的 RRF 负责，
	// 这里只是"这条路自己是按什么排的"。所以它叫 raw：拿它直接对外排序是错的。
	RawScore float64 `gorm:"column:raw_score"`
}

// KnowledgeVectorQuery 是一次向量召回的入参。
type KnowledgeVectorQuery struct {
	// ModelID 只在该模型生成的向量里比。不同模型的向量处在不同的语义空间，
	// 混在一起算出来的余弦相似度没有意义，所以这一列必须是等值条件而不是可选过滤。
	ModelID uint64

	// Dimensions 是查询向量的维度，也是比较之前 CAST 的目标维度（embedding::vector(N)）。
	// 它必须与模型登记的维度一致：不一致时 CAST 会当场报维度错误。
	Dimensions int

	// Vector 是 pgvector 的文本字面量，例如 "[0.1,0.2]"（rag.vectorLiteral 的产物）。
	Vector string

	// Limit 是这一路返回的候选上限。调用方一般过采样几倍，再由融合排序收敛到 top_k。
	Limit int
}
