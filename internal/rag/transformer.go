package rag

import (
	"context"
	"fmt"
	"math"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/schema"
)

// transformer.go 切分器的 Eino 适配：Markdown → 一组切片文档。
//
// 切分算法本身仍是 chunker.go 的 Split（纯函数、零依赖），这里只做形状转换。
// 这样收录链路与将来的 Eino 流水线（indexer / parent-document 流程）用的是同一份
// 切分逻辑，不会分叉成两套 —— 那两套迟早会在"表格不被切开""标题归属"这类细节上
// 给出不同结果，而差异只会在检索质量上慢慢显形。
//
// 输出用 schema.Document 表示每个切片：Content 是切片正文，MetaData 带上回填知识库
// 需要的两个字段（见下面的键），DocumentsToChunks 是它的逆操作。
//
// ⚠️ 收录链路经 splitMarkdown 走这条路径（见下），所以它不是一个"写完没人用"的适配层：
// 切分只有一条实现路径，与将来 Eino 流水线里消费的 Transformer 是同一个对象。
const (
	// ChunkIndexMetaKey 是切片序号的元数据键（从 0 开始、同一父文档内连续）。
	// 名字与 knowledge_chunks.chunk_index 对齐：排障时一眼能对上，也省得再想一套命名。
	ChunkIndexMetaKey = "chunk_index"

	// HeadingMetaKey 是切片所属章节标题的元数据键。无章节归属时**不写这个键**
	// （而不是写空串）：调用方用"键在不在"判断有没有标题，不必再判一次空。
	HeadingMetaKey = "heading"
)

// MarkdownChunker 把 Markdown 文档切成切片文档，实现 Eino 的 document.Transformer。
type MarkdownChunker struct {
	options ChunkOptions
}

// 编译期确认适配器满足 Eino 的 Transformer；上游接口签名变动时这里先编译失败。
var _ document.Transformer = (*MarkdownChunker)(nil)

// NewMarkdownChunker 创建切分器。options 为零值时用 chunker.go 的默认参数
// （Split 自己会把零值补成默认值，这里不再抄一份）。
func NewMarkdownChunker(options ChunkOptions) *MarkdownChunker {
	return &MarkdownChunker{options: options}
}

// Transform 逐个文档切分，输出顺序是"文档顺序 → 文档内切片顺序"。
//
// 空白正文不产出切片、也不报错：这里的职责只是切分，"空正文算不算失败"由收录门面判断
// （见 ingestMarkdown 里的 ErrEmptyContent）—— 同一份切分器给别处用时，空输入未必是错误。
func (c *MarkdownChunker) Transform(
	ctx context.Context,
	src []*schema.Document,
	_ ...document.TransformerOption,
) ([]*schema.Document, error) {
	_ = ctx

	var out []*schema.Document
	for _, parent := range src {
		if parent == nil {
			continue
		}
		for _, chunk := range Split(parent.Content, c.options) {
			out = append(out, chunkDocument(parent, chunk))
		}
	}
	return out, nil
}

// chunkDocument 把一块切片表示成 Eino 文档。
//
// 父文档的 MetaData 原样带上：Eino 对 Transformer 的约定是"保留既有元数据、只做合并"，
// 丢掉它会让下游（例如按来源过滤的检索）拿不到本该一路传下去的字段。
func chunkDocument(parent *schema.Document, chunk Chunk) *schema.Document {
	metadata := make(map[string]any, len(parent.MetaData)+2)
	for key, value := range parent.MetaData {
		metadata[key] = value
	}
	metadata[ChunkIndexMetaKey] = chunk.Index
	if chunk.Heading != "" {
		metadata[HeadingMetaKey] = chunk.Heading
	}
	return &schema.Document{
		ID:       chunkDocumentID(parent.ID, chunk.Index),
		Content:  chunk.Content,
		MetaData: metadata,
	}
}

// chunkDocumentID 给切片造一个稳定且唯一的 ID：父文档 ID + 序号。
//
// 父文档没有 ID 时只用序号：单次 Transform 内仍然唯一，但跨文档就分不开了 ——
// 依赖 ID 唯一的流程（例如 Eino 的 parent indexer）应当先给父文档一个 ID。
func chunkDocumentID(parentID string, index int) string {
	return fmt.Sprintf("%s#%d", parentID, index)
}

// DocumentsToChunks 把切片文档转回本模块的 Chunk（MarkdownChunker 的逆操作）。
//
// 它读 MarkdownChunker 写入的元数据；**序号缺失或非法一律报错**，不按出现顺序补号 ——
// 补号会把"元数据丢了"变成"顺序悄悄换了"，而 chunk_index 决定检索命中后如何还原上下文，
// 数值错了只会表现为"引用读起来串了"，几乎不可能查。缺标题不算错（那本来就是可选信息）。
func DocumentsToChunks(documents []*schema.Document) ([]Chunk, error) {
	chunks := make([]Chunk, 0, len(documents))
	for position, item := range documents {
		if item == nil {
			continue
		}
		index, err := chunkIndexFrom(item, position)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, Chunk{
			Index:   index,
			Heading: chunkHeadingFrom(item),
			Content: item.Content,
		})
	}
	return chunks, nil
}

// splitMarkdown 是收录链路用的切分入口：走 Eino 的 document.Transformer 切一遍，
// 再折回本模块的 Chunk。
//
// 为什么不直接调 Split：切分只留一条实现路径（见文件注释），适配器也就不会成为
// "写完没人调用"的死代码 —— 那种代码在真需要它的那天，往往已经和调用方的期待对不上了。
// 代价是一次 Chunk → schema.Document → Chunk 的往返，纯内存、无 IO。
//
// 返回的错误只可能来自 DocumentsToChunks（元数据契约被改坏）—— 那是内部一致性问题，
// 冒泡到收录链路会以 stage=chunk 落库，正好指认是切分这一环。
func splitMarkdown(ctx context.Context, markdown string, options ChunkOptions) ([]Chunk, error) {
	documents, err := NewMarkdownChunker(options).Transform(ctx, []*schema.Document{{Content: markdown}})
	if err != nil {
		return nil, fmt.Errorf("切分文档失败: %w", err)
	}
	return DocumentsToChunks(documents)
}

// chunkIndexFrom 读切片序号，读不到或不是合法序号时报错。
func chunkIndexFrom(item *schema.Document, position int) (int, error) {
	raw, exists := item.MetaData[ChunkIndexMetaKey]
	if !exists {
		return 0, fmt.Errorf("第 %d 个切片文档缺少 %q 元数据（ID=%q）",
			position+1, ChunkIndexMetaKey, item.ID)
	}
	index, ok := normalizeChunkIndex(raw)
	if !ok {
		return 0, fmt.Errorf("%q 不是合法的切片序号（ID=%q，实际是 %T: %v）",
			ChunkIndexMetaKey, item.ID, raw, raw)
	}
	return index, nil
}

// normalizeChunkIndex 把一个元数据值折成合法的切片序号（≥ 0 的整数）。
//
// 数值类型放宽到 int / int32 / int64 / float64，是因为切片文档可能经过一次 JSON 序列化
// （Eino 的图之间、日志与回放），那时整数会变成 float64。NaN / Inf / 小数一律不算数：
// 它们只会来自被改坏的元数据，换算成 int 会得到一个看起来正常的错误序号。
func normalizeChunkIndex(raw any) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, value >= 0
	case int32:
		return int(value), value >= 0
	case int64:
		return int(value), value >= 0
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value != math.Trunc(value) {
			return 0, false
		}
		return int(value), true
	default:
		return 0, false
	}
}

// chunkHeadingFrom 读章节标题，没有或不是字符串时按"没有标题"处理。
//
// 这里刻意不报错：标题只影响引用时的可读性，不影响顺序与归属，
// 为它让整批切片失败不划算。
func chunkHeadingFrom(item *schema.Document) string {
	if heading, ok := item.MetaData[HeadingMetaKey].(string); ok {
		return heading
	}
	return ""
}
