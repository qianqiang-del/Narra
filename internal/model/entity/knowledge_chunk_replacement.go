package entity

import "encoding/json"

// ChunkReplacement 是一次切片替换的全部输入。
//
// 它放在 entity 而不是仓储层，是因为它本身就是 KnowledgeDocument /
// KnowledgeChunk / KnowledgeEmbedding 三个实体的打包 —— 分开放在两个包里时，
// 收录链路（internal/rag）就必须为了拿到这个类型去 import 仓储包。
//
// 原文、摘要、切片、向量放在一个结构里传，是为了让它们天然属于同一个事务 ——
// 分成多次调用时，"正文已经更新、切片还没写"这种中间态会真的暴露给并发读取。
type ChunkReplacement struct {
	Title      string          // 文档标题（解析后可能会修正，例如用文件里的首个标题）
	Content    string          // 未经切分的完整正文，重建切片的唯一来源
	Checksum   string          // 正文的 SHA-256 摘要，用于判断是否需要重新切片
	Metadata   json.RawMessage // 写入文档的 metadata；为空表示不改
	Chunks     []KnowledgeChunk
	Embeddings []KnowledgeEmbedding // 与 Chunks 一一对应，ChunkID 由仓储回填
}
