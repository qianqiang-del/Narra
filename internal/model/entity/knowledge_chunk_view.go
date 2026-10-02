package entity

import "time"

// 这里的几个类型都不落库、不建表：它们是检索链路（internal/rag）与检索仓储
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
	SectionPath    *string `gorm:"column:section_path"`    // 所属节的完整路径；前言/无标题文档/代码为 NULL
	Symbol         *string `gorm:"column:symbol"`          // 代码切片对应的符号；非代码或提不出来时为 NULL
	Content        string  `gorm:"column:content"`         // 切片正文
	CharacterCount int32   `gorm:"column:character_count"` // 切片字符数
	DocumentTitle  string  `gorm:"column:document_title"`  // 所属文档标题
	SourceType     string  `gorm:"column:source_type"`     // 所属文档的来源类型
	SourceURI      *string `gorm:"column:source_uri"`      // 所属文档的来源标识；手工录入时为 NULL

	// RawScore 是这一行在**所属召回路**里的原始得分：向量路是余弦相似度（越近越大），
	// 词法路是加权命中分（命中精确词 > 词典词 > 兜底二元组，短语另计）。
	//
	// 两条路的口径不在一个量纲上，跨路比较没有意义 —— 融合排序由 rag 的 RRF 负责，
	// 这里只是"这条路自己是按什么排的"。所以它叫 raw：拿它直接对外排序是错的。
	RawScore float64 `gorm:"column:raw_score"`
}

// KnowledgeChunkText 是"装配上下文"用的最小切片形状：只要序号与正文。
//
// 与 KnowledgeChunkView 分开的理由：上下文装配按节/符号/邻域回读同组的切片，
// 只关心顺序与文本，不需要文档标题、来源、得分这些召回字段，也不需要 JOIN 文档表。
// 底线过滤（ready + enabled）仍在 SQL 里，见仓储的 listChunkTexts。
type KnowledgeChunkText struct {
	ChunkIndex int32  `gorm:"column:chunk_index"` // 切片在原文中的顺序号
	Content    string `gorm:"column:content"`     // 切片正文
}

// KnowledgeChunkFilter 是召回阶段两条路共用的过滤条件。
//
// 它过滤的对象是"这片切片来自哪篇文档"（条件都落在 knowledge_documents 上），
// 不是切片自身的正文；两条 SQL 本来就 JOIN 文档表，条件天然可以共享。
//
// 零值表示"这一项不参与过滤"，全零值等价于当前的不过滤行为 —— 调用方不必
// 自己判断"有没有条件"。条件之间是 AND，列表条件内部是 OR（IN）。
type KnowledgeChunkFilter struct {
	// SourceTypes 只看这些来源类型的文档（manual / import）。空 = 不限。
	SourceTypes []string

	// DocumentIDs 只看这些文档下的切片。空 = 不限。
	DocumentIDs []uint64

	// CreatedFrom 是文档创建时间的下界，含。nil = 不限。
	CreatedFrom *time.Time

	// CreatedTo 是文档创建时间的上界，不含。nil = 不限。
	// 用半开区间是为了避开"23:59:59.999"这种边界补丁：上界写成下一天零点即可。
	CreatedTo *time.Time
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

	// Filter 是文档侧的过滤条件，两条召回路必须带同一份（零值 = 不过滤）。
	Filter KnowledgeChunkFilter
}

// KnowledgeLexicalTerm 是词法路的一个带权词项。
//
// 权重由检索侧的分词层给出（精确词 > 词典词 > 兜底二元组，短语最高），决定"命中什么"
// 比"命中几个"更值钱；它只影响同一条召回路内部的排序，跨路融合仍由 RRF 按名次进行。
type KnowledgeLexicalTerm struct {
	Text   string  // 词项文本，非空、已去重
	Weight float64 // 权重；必须为正，数值大小只有相对意义
}

// KnowledgeLexicalQuery 是一次词法召回的入参。
//
// 与 KnowledgeVectorQuery 分列两个类型（而不是给 SearchLexical 加参数）：
// 词法路的东西只会越来越多（词项权重、匹配字段），一次一个参数签不住 ——
// 权重与短语就是先落在这里的。
type KnowledgeLexicalQuery struct {
	// Terms 参与召回：逐项匹配、命中任意一项即入选。空时返回空结果。
	Terms []KnowledgeLexicalTerm

	// Phrases 只参与打分：命中词项的前提下，整段原样出现额外加权重。
	// 它不进召回的准入条件 —— 短语是精度信号，不是召回信号。
	Phrases []KnowledgeLexicalTerm

	// Limit 是这一路返回的候选上限。
	Limit int

	// Filter 与向量路共用同一份过滤条件（零值 = 不过滤）。
	Filter KnowledgeChunkFilter
}
