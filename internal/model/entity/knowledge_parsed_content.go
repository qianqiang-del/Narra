package entity

import "encoding/json"

// ParsedContent 是一次文件解析的产物，供仓储把它写回文档行并推进收录阶段。
//
// 它是分阶段收录的第一步：保存成功后 ingest_stage 推进到 chunk。此后即使进程崩溃、
// 向量服务故障，恢复都不再需要读原文件重新解析 —— 那是整条链路里最贵的一步。
//
// 它放在 entity 而不是仓储层，理由与 ChunkReplacement 相同：收录链路（internal/rag）
// 要用它，而 rag 不该 import 仓储包。
type ParsedContent struct {
	Title    string          // 文档标题（解析后可能会修正，例如用文件里的首个标题）
	Content  string          // 解析出的 Markdown 正文
	Checksum string          // 正文的 SHA-256 摘要，列类型是 char(64)
	Metadata json.RawMessage // 合并进文档 metadata 的顶层键；为空表示不改
}
