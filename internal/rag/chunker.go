// Package rag 是知识库能力的运行时，与 internal/mcp 是同一层的兄弟模块。
//
// 按职责分文件，各管一段：
//   - ingest.go   收录门面：解析 → 切分 → 向量化 → 一个事务落三张表
//   - sections.go Markdown 结构识别：goldmark 解析出节树（取舍见该文件顶部）
//   - chunker.go  装填：预算、句子边界、重叠、节路径（本文件）
//   - embedder.go 向量化的最小依赖面与批量编排
//   - errors.go   收录链路的哨兵错误
//
// mcp 与 rag 的共同点：真正要 IO 的部分都通过**本包自己定义的小接口**进来
// （mcp 是 managedClient / clientFactory，这里是 DocumentStore / ModelRegistry /
// Embedder），实现放在仓储层与 pkg/，注入在 internal/app 完成。
// 接口窄是本包能脱离数据库和向量服务做完整测试的前提。
//
// 分工的另一半在 internal/service：MCP 的配置增删改查归 mcp_server_service.go，
// 知识库的文档列表与详情归 knowledge_service.go。那里是 HTTP 面，只碰 DTO；
// 本包只碰 entity —— 两边各说各的话，谁也不迁就谁。
//
// ⚠️ 切分链路（本文件 + sections.go）只做纯内存的事：不读文件、不认识数据库、
// 不发网络请求。结构识别交给 goldmark（纯内存解析库），装填策略仍是手写的纯函数 ——
// 切片质量的问题几乎都能用一段 Markdown 复现，放进这条链路里测，
// 比起连上数据库和向量服务再去调要快得多。
//
// 切分的三个约束来自数据库本身（见 entity.KnowledgeChunk 的字段 tag），它们不是
// 建议而是硬约束，违反会直接被 PostgreSQL 拒绝：
//   - character_count > 0    —— 绝不能产出空白切片；
//   - chunk_index >= 0       —— 序号从 0 开始，且在同一篇文章内唯一；
//   - heading / section_path varchar(300) —— 标题与路径都按字符截断，
//     中文一个字符占三个字节，按字节截会爆。
package rag

import (
	"strings"
	"unicode/utf8"
)

// MaxHeadingRunes 是标题的长度上限，与 knowledge_chunks.heading（以及 section_path）
// 的 varchar(300) 对齐。
//
// 超长标题在这里直接截断而不是报错：标题只是检索时给人和模型看的上下文线索，
// 为了它让整篇文档入库失败不划算。
const MaxHeadingRunes = 300

// Chunk 是一个切片。Index 从 0 开始连续，直接对应 knowledge_chunks.chunk_index。
type Chunk struct {
	Index       int    // 在原文中的顺序
	Heading     string // 所在节的标题；前言切片为空
	SectionPath string // 从顶层到本级的标题路径（用 / 连接）；前言切片为空
	Content     string // 实际参与向量化和检索的文本
}

// ChunkOptions 是切分参数。零值由 withDefaults 补成 DefaultXxx。
//
// 名字里带 Chunk 是因为本包还管向量化：Options 这种泛称摆在 rag.IngestResult、
// rag.Embedder 旁边会指代不明，而这里要调的确实只是切片的字符预算与重叠。
type ChunkOptions struct {
	// MaxChars 单个切片的字符预算（按 rune 计，与 PostgreSQL 的 char_length 一致）。
	//
	// 800 是个折中：多数向量模型的有效上下文远大于它，而检索时切片太长会把
	// 不相关的内容一起带进提示词，反而稀释相关性。
	MaxChars int
	// MinChars 期望的最小切片长度。小于它的收尾切片会并回上一片，
	// 避免文章末尾出现一个只有几个字的碎片。
	MinChars int
	// Overlap 相邻切片的重叠字符数。表格被切断、概念跨页这类情况下，
	// 没有重叠会让边界处的信息两边都取不到。
	Overlap int
}

const (
	// DefaultMaxChars 默认切片字符预算。
	DefaultMaxChars = 800
	// DefaultMinChars 默认最小切片长度。
	DefaultMinChars = 120
	// DefaultOverlap 默认重叠字符数。
	DefaultOverlap = 120

	// oversizeTolerance 整块内容（代码块、表格）超过预算多少倍才允许被切开。
	//
	// 代码块和表格一旦从中间断开就失去意义，所以在容忍范围内宁可让这一片超预算。
	// 只有远超预算（默认 2 倍）时才会硬切。
	oversizeTolerance = 2
)

func (o ChunkOptions) withDefaults() ChunkOptions {
	if o.MaxChars <= 0 {
		o.MaxChars = DefaultMaxChars
	}
	if o.MinChars <= 0 {
		o.MinChars = DefaultMinChars
	}
	if o.MinChars > o.MaxChars {
		o.MinChars = o.MaxChars
	}
	if o.Overlap <= 0 {
		o.Overlap = DefaultOverlap
	}
	if o.Overlap > o.MaxChars/2 {
		// 重叠超过半个切片就没有信息增量了，只会让切片数量翻倍。
		o.Overlap = o.MaxChars / 2
	}
	return o
}

// contentLimit 是留给切片正文的预算：总预算先减去重叠，剩下的位置才是正文的。
//
// 之所以要给重叠预留，是因为切分和装填是两步：如果正文按总预算填满，
// 装填阶段就再也塞不进重叠前缀了 —— 实测中那样几乎永远产生不了重叠，
// 而"相邻切片带重叠"恰恰是切分质量的关键。
func (o ChunkOptions) contentLimit() int {
	limit := o.MaxChars - o.Overlap
	if limit < 1 {
		return o.MaxChars
	}
	return limit
}

// Split 把 Markdown 切成切片。返回的切片序号从 0 开始连续，
// 空白内容一律丢弃 —— 数据库的 character_count > 0 不接受空切片。
//
// 切法是"结构优先"的递归：sections.go 解析出节树后，一个节的**整棵子树**
// 不超过预算就整块出一片（哪怕里面还有子标题）；超预算才按它的子节递归，
// 一直递归到没有子节的叶子，才回到句子边界与预算来分。短小节的碎片因此自然消失，
// 长小节才被切开，切点也尽量落在标题这种语义边界上。
//
// 切片长度以 ChunkOptions.MaxChars 为目标，只有三种情况会略超：
// 整棵子树出的块、代码块与表格为保持完整而整块保留、以及过短的收尾并回上一片。
// 预算是给检索质量用的软目标，不是数据库约束，这些例外都比"为了凑数切碎"更值。
func Split(markdown string, options ChunkOptions) []Chunk {
	options = options.withDefaults()

	sections := parseSections(normalize(markdown))

	groups := make([][]emittedChunk, 0, len(sections))
	for _, section := range sections {
		if chunks := emitSection(section, options); len(chunks) > 0 {
			groups = append(groups, chunks)
		}
	}
	// 顶层不做"接缝重叠"、也不把文档前言并进第一章：两者与改造前的语义不同
	// （那时每个节独立打包，节与节之间不重叠、前言自成一节），保持原样更稳；
	// 过小的一级节仍会并进上一块，见 mergeTinyGroups。
	emitted := joinSiblingGroups(groups, options, false, false)

	chunks := make([]Chunk, 0, len(emitted))
	for _, item := range emitted {
		content := item.chunk.content()
		if content == "" {
			continue
		}
		chunks = append(chunks, Chunk{
			Heading:     truncateHeading(item.heading),
			SectionPath: truncateHeading(item.path),
			Content:     content,
		})
	}

	// 序号在最后统一编：中间任何一步丢弃了切片，这里都能保证仍然连续，
	// 而 UNIQUE (document_id, chunk_index) 要求它既不重复也不跳号。
	for index := range chunks {
		chunks[index].Index = index
	}
	return chunks
}

// normalize 统一换行并把 BOM 去掉。
//
// CRLF 不去掉的话，句子切分时 "\r" 会混进切片内容，落到库里就是看不见的脏字符；
// BOM 会让第一个切片以一个零宽字符开头。
func normalize(markdown string) string {
	markdown = strings.TrimPrefix(markdown, "\ufeff")
	markdown = strings.ReplaceAll(markdown, "\r\n", "\n")
	return strings.ReplaceAll(markdown, "\r", "\n")
}

// block 是切分的最小输入单元。atomic 表示它不该被从中间切开（代码块、表格）。
type block struct {
	text   string
	atomic bool
}

// blockOf 构造一个块，text 统一去掉首尾空白 —— 否则后面的字符预算会把空行也算进去。
func blockOf(text string, atomic bool) block {
	return block{text: strings.TrimSpace(text), atomic: atomic}
}

// emittedChunk 是一条已经装填好、但还没统一编号的切片：它记得自己属于哪个节。
type emittedChunk struct {
	heading string
	path    string
	chunk   pendingChunk
}

// emitSection 产出一个节（含子树）的全部切片。规则见 Split 的注释。
//
// 返回的列表内部已经按"相邻兄弟组"补好重叠；对上层而言它是一个整体，
// 只会在组与组的接缝处被上层再补一次前缀 —— 每个接缝只补一次，不会重复。
func emitSection(node *sectionNode, options ChunkOptions) []emittedChunk {
	size := subtreeRunes(node)
	if size == 0 {
		return nil
	}
	// 整棵子树装得下：一块出。子标题不再开新片 —— 短小节合成一片，
	// 比按标题切成一堆碎片更适合检索。
	if size <= options.MaxChars {
		return []emittedChunk{wholeSectionChunk(node)}
	}
	// 叶子：没有更细的标题可用，回到句子边界与预算。
	if len(node.children) == 0 {
		return wrapChunks(node, packBlocks(node.blocks, options))
	}

	groups := make([][]emittedChunk, 0, len(node.children)+1)
	if len(node.blocks) > 0 {
		groups = append(groups, wrapChunks(node, packBlocks(node.blocks, options)))
	}
	for _, child := range node.children {
		if chunks := emitSection(child, options); len(chunks) > 0 {
			groups = append(groups, chunks)
		}
	}
	return joinSiblingGroups(groups, options, true, true)
}

// wholeSectionChunk 把整棵子树拼成一条切片（不再分预算，由调用方保证它装得下）。
func wholeSectionChunk(node *sectionNode) emittedChunk {
	pieces := make([]piece, 0, 4)
	collectPieces(node, &pieces)
	return emittedChunk{
		heading: node.heading,
		path:    node.path,
		chunk:   pendingChunk{parts: pieces},
	}
}

// collectPieces 按文档顺序把子树里所有块翻成片段（块之间用空行分隔）。
func collectPieces(node *sectionNode, pieces *[]piece) {
	for _, item := range node.blocks {
		text := strings.TrimSpace(item.text)
		if text == "" {
			continue
		}
		separator := "\n\n"
		if len(*pieces) == 0 {
			separator = ""
		}
		*pieces = append(*pieces, piece{text: text, sep: separator})
	}
	for _, child := range node.children {
		collectPieces(child, pieces)
	}
}

// wrapChunks 给一个节自己产出的切片贴上节归属。
func wrapChunks(node *sectionNode, chunks []pendingChunk) []emittedChunk {
	out := make([]emittedChunk, 0, len(chunks))
	for _, chunk := range chunks {
		out = append(out, emittedChunk{heading: node.heading, path: node.path, chunk: chunk})
	}
	return out
}

// subtreeRunes 统计一棵子树的内容长度（按字符计，块之间的空行也算 ——
// 它是"整块出"的判据，口径要与最终拼出来的文本尽量一致）。
func subtreeRunes(node *sectionNode) int {
	total, count := 0, 0
	var walk func(current *sectionNode)
	walk = func(current *sectionNode) {
		for _, item := range current.blocks {
			text := strings.TrimSpace(item.text)
			if text == "" {
				continue
			}
			total += utf8.RuneCountInString(text)
			count++
		}
		for _, child := range current.children {
			walk(child)
		}
	}
	walk(node)
	if count > 1 {
		total += 2 * (count - 1)
	}
	return total
}

// joinSiblingGroups 把同一层下若干兄弟组的切片按文档顺序拼成一条链。
//
// 先并掉过小的小组（mergeTinyGroups），再按需在组与组的接缝处补重叠：
// 只给每一组的**第一片**补前缀，组内部的接缝在产出该组的那一层已经补过，
// 这里再补就会让同一段文字重复三遍。两个开关都由调用方按层级决定：
// 顶层既不补跨章节重叠也不把文档前言并进第一章（保持改造前的语义）。
func joinSiblingGroups(groups [][]emittedChunk, options ChunkOptions, seedAcrossGroups, mergeHead bool) []emittedChunk {
	merged := mergeTinyGroups(groups, options, mergeHead)

	out := make([]emittedChunk, 0, len(merged))
	for _, group := range merged {
		if len(group) == 0 {
			continue
		}
		if seedAcrossGroups && len(out) > 0 {
			budget := options.MaxChars - utf8.RuneCountInString(group[0].chunk.content())
			group[0].chunk.seed = overlapSeed(out[len(out)-1].chunk.content(), options.Overlap, budget)
		}
		out = append(out, group...)
	}
	return out
}

// mergeTinyGroups 把过小的兄弟组并进相邻的组。
//
// 一节的子树太小（< MinChars）时单独成片没有检索价值，还会让它在向量空间里随机地飘；
// 幻灯片式的文档（每页一个小标题）正是靠这条规则合出像样的切片。
// 只并"单块的小组"，且合并后不超过 MaxChars；合并后保留接收块的标题与路径 ——
// 合并块横跨两节，保留接收方的归属是简单且可预期的选择。
//
// 两个方向都要处理：后组并进前组（常见），以及**第一组**（节的引言）过小时
// 并进紧随其后的组 —— 它没有"前一组"可并，见 mergeTinyHead。
// mergeHead 由调用方按层级给：文档前言（顶层第一组）不参与，保持改造前的独立切片。
func mergeTinyGroups(groups [][]emittedChunk, options ChunkOptions, mergeHead bool) [][]emittedChunk {
	out := make([][]emittedChunk, 0, len(groups))
	for _, group := range groups {
		if len(group) == 0 {
			continue
		}
		if len(group) == 1 && len(out) > 0 {
			small := group[0].chunk.content()
			previousGroup := out[len(out)-1]
			previousChunk := &previousGroup[len(previousGroup)-1]
			// 不把有节归属的内容埋进"没有节"的前言块里：前言自成一片是改造前的语义，
			// 归属也更可读；有标题的相邻小节之间照常合并（幻灯片式文档靠这条）。
			canMerge := previousChunk.path != "" || group[0].path == ""
			if canMerge && utf8.RuneCountInString(small) < options.MinChars {
				previous := &previousChunk.chunk
				if utf8.RuneCountInString(previous.content())+2+utf8.RuneCountInString(small) <= options.MaxChars {
					previous.parts = append(previous.parts, piece{text: small, sep: "\n\n"})
					continue
				}
			}
		}
		out = append(out, group)
	}
	if !mergeHead {
		return out
	}
	return mergeTinyHead(out, options)
}

// mergeTinyHead 处理"第一组过小"：把它并进下一组的第一个块。
// 前言与引言都坐在这个位置 —— 后面紧跟着正文，单独立片只是碎片。
func mergeTinyHead(groups [][]emittedChunk, options ChunkOptions) [][]emittedChunk {
	if len(groups) < 2 || len(groups[0]) != 1 {
		return groups
	}
	small := groups[0][0].chunk.content()
	if utf8.RuneCountInString(small) >= options.MinChars {
		return groups
	}
	target := &groups[1][0]
	if utf8.RuneCountInString(small)+2+utf8.RuneCountInString(target.chunk.content()) > options.MaxChars {
		return groups
	}

	// 插到目标块最前面，并给目标块原来的首个片段补一个段落分隔符：
	// content() 会忽略"首片且无前缀"那个片段的分隔符，不补就贴在一起了。
	pieces := make([]piece, 0, len(target.chunk.parts)+1)
	pieces = append(pieces, piece{text: small})
	for index, part := range target.chunk.parts {
		if index == 0 {
			part.sep = "\n\n"
		}
		pieces = append(pieces, part)
	}
	target.chunk.parts = pieces
	return groups[1:]
}

// packBlocks 把一个节直属的块打包成切片：先按句子拆片段，再按预算装填，
// 最后处理过短的收尾与相邻重叠。
func packBlocks(blocks []block, options ChunkOptions) []pendingChunk {
	pieces := blockPieces(blocks, options)
	if len(pieces) == 0 {
		return nil
	}

	packed := pack(pieces, options)
	packed = mergeTail(packed, options)

	out := make([]pendingChunk, 0, len(packed))
	for _, chunk := range packed {
		if strings.TrimSpace(chunk.content()) == "" {
			continue
		}
		out = append(out, chunk)
	}
	return out
}

// blockPieces 把一个节直属的块拆成不超过预算的片段。
func blockPieces(blocks []block, options ChunkOptions) []piece {
	var pieces []piece
	for index, current := range blocks {
		// 首块的首片不需要前导分隔符，其余块之间用空行隔开。
		separator := "\n\n"
		if index == 0 {
			separator = ""
		}
		for position, text := range splitBlock(current, options) {
			if strings.TrimSpace(text) == "" {
				continue
			}
			// 同一个块被切开后的后续片段，是同一段话的延续，拼接时不留空行。
			useSeparator := separator
			if position > 0 || len(pieces) == 0 {
				useSeparator = ""
			}
			pieces = append(pieces, piece{text: text, sep: useSeparator})
		}
	}
	return pieces
}

// piece 是切片内部的一个片段。sep 是把它接到前一片段后面时用的分隔符：
// 同一段落被句子切开的片段用空串拼接（拼回去还是原来那段话），
// 不同段落之间用空行。
type piece struct {
	text string
	sep  string
}

// pendingChunk 是一个尚未定稿的切片。
//
// seed 是承上一片结尾的重叠内容；parts 是本片自己的片段。分开存是为了让
// "把过短的收尾切片并回上一片"可以只搬 parts、丢掉重复的 seed。
type pendingChunk struct {
	seed  string
	parts []piece
}

// content 拼出切片的最终文本。
func (c pendingChunk) content() string {
	var builder strings.Builder
	if c.seed != "" {
		builder.WriteString(c.seed)
	}
	for index, part := range c.parts {
		switch {
		case index == 0 && c.seed == "":
			// 首片且没有重叠前缀：直接写，忽略 sep（它本来是给段落之间用的）
		case index == 0:
			builder.WriteString("\n\n")
		default:
			builder.WriteString(part.sep)
		}
		builder.WriteString(part.text)
	}
	// 片段是按句子边界切开的，两端可能带着空格和换行。
	return strings.TrimSpace(builder.String())
}

// splitBlock 把一个块拆成若干不超过正文预算的片段。
func splitBlock(current block, options ChunkOptions) []string {
	text := strings.TrimSpace(current.text)
	if text == "" {
		return nil
	}

	limit := options.contentLimit()
	length := utf8.RuneCountInString(text)
	if length <= limit {
		return []string{text}
	}
	// 代码块与表格：只要没夸张到 oversizeTolerance 倍，宁可超预算也不断开。
	if current.atomic && length <= options.MaxChars*oversizeTolerance {
		return []string{text}
	}

	// 段落级的超长文本按句子切开。切得碎没有坏处 —— 下面的装填会立刻把它们
	// 按预算拼回去，只有"单句本身就超预算"时才会真的留下断口。
	var parts []string
	var buffer strings.Builder
	bufferLength := 0

	for _, sentence := range splitSentences(text) {
		length := utf8.RuneCountInString(sentence)
		if bufferLength > 0 && bufferLength+length > limit {
			parts = append(parts, buffer.String())
			buffer.Reset()
			bufferLength = 0
		}
		if length > limit {
			// 整句没有标点（例如一长串没有分隔的代码），只能硬切。
			parts = append(parts, hardSplit(sentence, limit)...)
			continue
		}
		buffer.WriteString(sentence)
		bufferLength += length
	}
	if rest := buffer.String(); strings.TrimSpace(rest) != "" {
		parts = append(parts, rest)
	}
	return parts
}

// splitSentences 按句末标点切开文本，标点保留在句尾。
//
// 中英文标点都算，且故意切得激进（"." 也算句末）：切碎了会被装填阶段拼回去，
// 只有"单句过长"这一种情况下切分位置才会真的影响结果，激进反而更安全。
func splitSentences(text string) []string {
	var sentences []string
	var buffer strings.Builder

	for _, symbol := range text {
		buffer.WriteRune(symbol)
		if isSentenceEnd(symbol) {
			sentences = append(sentences, buffer.String())
			buffer.Reset()
		}
	}
	if buffer.Len() > 0 {
		sentences = append(sentences, buffer.String())
	}
	return sentences
}

// isSentenceEnd 判断一个字符能不能当句子边界，中英文标点都算，换行也算 ——
// 硬折行的段落里行尾本来就是一个天然停顿。overlapSeed 也用它来把重叠的起点
// 对齐到句首，所以这里的取值直接决定重叠前缀从哪儿开始。
func isSentenceEnd(symbol rune) bool {
	switch symbol {
	case '。', '！', '？', '；', '…', '\n', '.', '!', '?', ';':
		return true
	default:
		return false
	}
}

// hardSplit 在没有任何可用语义边界时按行、再按字符硬切。
func hardSplit(text string, maxChars int) []string {
	if utf8.RuneCountInString(text) <= maxChars {
		return []string{text}
	}

	var parts []string
	var buffer strings.Builder
	bufferLength := 0

	for _, line := range strings.Split(text, "\n") {
		length := utf8.RuneCountInString(line)
		if bufferLength > 0 && bufferLength+1+length > maxChars {
			parts = append(parts, buffer.String())
			buffer.Reset()
			bufferLength = 0
		}
		if length > maxChars {
			runes := []rune(line)
			for len(runes) > maxChars {
				parts = append(parts, string(runes[:maxChars]))
				runes = runes[maxChars:]
			}
			line, length = string(runes), len(runes)
		}
		if length == 0 {
			continue
		}
		if bufferLength > 0 {
			buffer.WriteString("\n")
			bufferLength++
		}
		buffer.WriteString(line)
		bufferLength += length
	}
	if rest := buffer.String(); strings.TrimSpace(rest) != "" {
		parts = append(parts, rest)
	}
	return parts
}

// pack 把片段按预算装填成切片，再给相邻切片补上重叠。
func pack(pieces []piece, options ChunkOptions) []pendingChunk {
	limit := options.contentLimit()

	var chunks []pendingChunk
	current := pendingChunk{}
	currentLength := 0

	for _, next := range pieces {
		length := utf8.RuneCountInString(next.text)
		separatorLength := 0
		if len(current.parts) > 0 {
			separatorLength = utf8.RuneCountInString(next.sep)
		}

		if len(current.parts) > 0 && currentLength+separatorLength+length > limit {
			chunks = append(chunks, current)
			current = pendingChunk{}
			currentLength = 0
			separatorLength = 0
		}

		current.parts = append(current.parts, next)
		currentLength += separatorLength + length
	}
	if len(current.parts) > 0 {
		chunks = append(chunks, current)
	}

	// 重叠放在装填之后补，而不是在开新片时先塞进去：正文先按正文预算排好，
	// 剩下的差额才是留给前缀的。顺序反过来（先塞前缀再装正文）会让前缀挤占
	// 正文位置，每片都装不满，而且很容易因为余量不足而永远产生不了重叠。
	for index := 1; index < len(chunks); index++ {
		budget := options.MaxChars - utf8.RuneCountInString(chunks[index].content())
		chunks[index].seed = overlapSeed(chunks[index-1].content(), options.Overlap, budget)
	}
	return chunks
}

// overlapSeed 取上一片结尾的一段文字，作为下一片的开头。
//
// budget 是下一片留给前缀的余量：前缀挤占预算是必然的，但如果余量为负说明
// 下一片自己就快满了，这时不该再塞前缀。
//
// 起点会往后挪到最近的句末标点之后，避免从半个词或半句话开始 ——
// 那种开头对检索没有帮助，反而容易让模型把断口当成原文。
func overlapSeed(content string, overlap, budget int) string {
	if overlap <= 0 || budget <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(content))
	if len(runes) <= overlap {
		// 上一片本身就很短，整段复制一遍只会让同一段文字被检索到两次。
		return ""
	}

	limit := overlap
	if limit > budget {
		limit = budget
	}
	start := len(runes) - limit
	for start < len(runes) && !isSentenceEnd(runes[start]) {
		start++
	}
	if start >= len(runes) {
		return ""
	}
	start++ // 跳过标点本身，让前缀从新句子开头起算
	return strings.TrimSpace(string(runes[start:]))
}

// mergeTail 把过短的收尾切片并回上一片。
//
// 收尾碎片（例如文章末尾只剩一句"以上。"）单独成片没有检索价值，还会让它
// 在向量空间里随机地飘到某个位置。合并时丢掉它的 seed：seed 本来就是上一片的
// 结尾，留着会让同一段文字在库里出现两次。
//
// 各片段自带的分隔符原样保留，不改成空行：同一个段落被硬切开的两段本来就是
// 连着的，硬插一个空行会把原文改得面目全非。
func mergeTail(chunks []pendingChunk, options ChunkOptions) []pendingChunk {
	if len(chunks) < 2 {
		return chunks
	}

	last := chunks[len(chunks)-1]
	if utf8.RuneCountInString(last.content()) >= options.MinChars {
		return chunks
	}

	previous := chunks[len(chunks)-2]
	merged := make([]piece, 0, len(previous.parts)+len(last.parts))
	merged = append(merged, previous.parts...)
	merged = append(merged, last.parts...)

	candidate := pendingChunk{seed: previous.seed, parts: merged}
	if utf8.RuneCountInString(candidate.content()) > options.MaxChars+options.Overlap {
		return chunks
	}

	out := make([]pendingChunk, 0, len(chunks)-1)
	out = append(out, chunks[:len(chunks)-2]...)
	out = append(out, candidate)
	return out
}

// truncateHeading 按字符截断标题/节路径，与 varchar(300) 的语义一致。
func truncateHeading(heading string) string {
	heading = strings.TrimSpace(heading)
	runes := []rune(heading)
	if len(runes) <= MaxHeadingRunes {
		return heading
	}
	return string(runes[:MaxHeadingRunes])
}
