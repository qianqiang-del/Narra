package rag

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.uber.org/zap"

	"narra/internal/model/entity"
	"narra/pkg/logger"
)

// assemble.go 是检索结果的"上下文装配层"：两层 RRF 融合与精排之后、交付之前，
// 把同一组的多条命中折叠成一条，并把整组文本拼好放进 Hit.Context。
//
// 为什么包在**精排外面**（而不是插在精排之前）：
//   - 精排的输入与评测口径不能动：它按"单张切片"打分，先折叠会让候选池变小，
//     "把埋在中间的切片捞上来"的能力被削弱；
//   - "选哪片当代表"应该由精排的名次决定（同一组里分最高的那片）；
//   - 展开只对最终留下的 top_k 条做，工作量最小。
//
// 分组规则（按优先级）：
//   - 有 SectionPath → 同一节一组（文档）；
//   - 无节但有 Symbol → 同一符号一组（代码）；
//   - 两者都空 → 不折叠，单片参加（无标题文本、前言、无符号代码）。
//
// 折叠就是"选代表"的规则：按精排顺序往下走，每组只收第一条，收满 top_k 停 ——
// 回填不是另一步，跳过重复继续往下取就是回填。展开只发生在选完之后。
//
// 降级：查库或拼接失败只让这一条的 Context 留空并记一条 Warn，检索结果照常返回 ——
// 装配是加法，不该变成必经之路。

const (
	// assemblePerHitMaxChars 单条 Context 的字数上限。
	//
	// 2000 字约等于两到三张切片：多数整节能完整装下；装不下的长节以命中切片为中心
	// 截窗口（见 selectWindow），保证命中点一定在上下文里。
	assemblePerHitMaxChars = 2000

	// assembleTotalMaxChars 一次检索所有 Context 的总上限。
	//
	// top_k 调大时（上限 50）不设总量会让上下文成倍膨胀；8000 字按默认 top_k=5
	// 平均下来每条 1600 字，是"材料完整"与"上下文预算"之间的起点，评测后再调。
	assembleTotalMaxChars = 8000

	// assembleWindowRadius 无节/无符号切片的邻域半径（前后各几块）。
	assembleWindowRadius = 1

	// maxTrimRunes 拼接时最多尝试裁掉多长的重叠。
	//
	// 切分重叠默认 120 字、可配置调大；200 是留了余量的上限。再长的"匹配"多半是
	// 巧合而不是重叠，裁掉它的风险大于收益。
	maxTrimRunes = 200
)

// ChunkAssembler 是装配层对持久化的最小依赖面；*repository.KnowledgeSearchRepository 满足它。
//
// 三个方法都是"按位置取文本"（不参与相似度），实现里带与召回一致的底线过滤
// （文档 ready + enabled），见仓储接口上的说明。
type ChunkAssembler interface {
	ListSectionChunkTexts(ctx context.Context, documentID uint64, sectionPath string) ([]entity.KnowledgeChunkText, error)
	ListSymbolChunkTexts(ctx context.Context, documentID uint64, symbol string) ([]entity.KnowledgeChunkText, error)
	ListChunkTextWindow(ctx context.Context, documentID uint64, from, to int32) ([]entity.KnowledgeChunkText, error)
}

// Assembled 是上下文装配装饰器：实现与内层相同的 Retrieve 签名，可直接替换给服务层使用。
type Assembled struct {
	inner  Searcher
	chunks ChunkAssembler
}

// NewAssembled 构造装配装饰器。
func NewAssembled(inner Searcher, chunks ChunkAssembler) (*Assembled, error) {
	if inner == nil {
		return nil, fmt.Errorf("上下文装配层需要非空的检索能力")
	}
	if chunks == nil {
		return nil, fmt.Errorf("上下文装配层需要非空的切片读取能力")
	}
	return &Assembled{inner: inner, chunks: chunks}, nil
}

// Retrieve 先让内层多取一些候选，折叠、回填到调用方要的条数，再补上下文。
//
// 内层要到的候选深度与精排一致（rerankCandidateDepth）：折叠会跳过重复，
// 候选必须有富余才能回填满 top_k；精排看到的输入与打分完全不变。
func (a *Assembled) Retrieve(ctx context.Context, input RetrieveInput) (RetrieveResult, error) {
	topK := clampTopK(input.TopK)

	depth := rerankCandidateDepth
	if topK > depth {
		depth = topK
	}
	innerInput := input
	innerInput.TopK = depth

	result, err := a.inner.Retrieve(ctx, innerInput)
	if err != nil {
		return RetrieveResult{}, err
	}

	result.Hits = a.assemble(ctx, foldByGroup(result.Hits, topK))
	return result, nil
}

// foldByGroup 按分组键折叠命中并回填到 topK：按传入顺序（精排名次）走，
// 每组只收第一条，收满 topK 停；顺序保持精排名次。
func foldByGroup(hits []Hit, topK int) []Hit {
	chosen := make([]Hit, 0, topK)
	seen := make(map[string]struct{}, topK)
	for _, hit := range hits {
		key := assemblyGroupKey(hit)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		chosen = append(chosen, hit)
		if len(chosen) == topK {
			break
		}
	}
	return chosen
}

// assemblyGroupKey 是一个命中所属组的稳定键。前缀区分三种组，避免不同组的键相撞。
func assemblyGroupKey(hit Hit) string {
	switch {
	case hit.SectionPath != "":
		return "s\x00" + strconv.FormatUint(hit.DocumentID, 10) + "\x00" + hit.SectionPath
	case hit.Symbol != "":
		return "y\x00" + strconv.FormatUint(hit.DocumentID, 10) + "\x00" + hit.Symbol
	default:
		// 没有组身份：每片自成一组，不折叠。
		return "c\x00" + strconv.FormatUint(hit.ChunkID, 10)
	}
}

// assemble 给每条命中补上下文，并控制整次检索的总预算。
func (a *Assembled) assemble(ctx context.Context, hits []Hit) []Hit {
	remaining := assembleTotalMaxChars
	for index := range hits {
		if remaining <= 0 {
			break
		}
		budget := assemblePerHitMaxChars
		if budget > remaining {
			budget = remaining
		}
		text, err := a.contextFor(ctx, hits[index], budget)
		if err != nil {
			logger.Warn("上下文装配失败，本条只返回命中切片",
				zap.Uint64("chunk_id", hits[index].ChunkID),
				zap.Error(err),
			)
			continue
		}
		hits[index].Context = text
		remaining -= utf8.RuneCountInString(text)
	}
	return hits
}

// contextFor 按分组类型回读整组切片并拼成上下文；拼出来与命中切片本身相同
// （单块组）时返回空串 —— 不白送一段重复文本。
func (a *Assembled) contextFor(ctx context.Context, hit Hit, budget int) (string, error) {
	var (
		rows []entity.KnowledgeChunkText
		trim bool
		err  error
	)
	switch {
	case hit.SectionPath != "":
		rows, err = a.chunks.ListSectionChunkTexts(ctx, hit.DocumentID, hit.SectionPath)
		trim = true // 文档链路的相邻切片带句首对齐的重叠，拼接时要裁掉
	case hit.Symbol != "":
		rows, err = a.chunks.ListSymbolChunkTexts(ctx, hit.DocumentID, hit.Symbol)
		trim = false // 代码切分不补重叠
	default:
		rows, err = a.chunks.ListChunkTextWindow(ctx, hit.DocumentID, hit.ChunkIndex-assembleWindowRadius, hit.ChunkIndex+assembleWindowRadius)
		trim = true
	}
	if err != nil {
		return "", err
	}
	return assembleContextText(rows, hit, budget, trim), nil
}

// assembleContextText 把同组切片按序拼成一段文本。
//
// 拼接规则：
//   - trim 为真（文档/窗口）：相邻片先裁重叠。裁掉了说明接缝本来就是连续的，
//     直接相连；没裁掉说明多半是段落边界，用空行隔开 —— 宁可多一个空行，
//     也不把两段话粘成一句。
//   - trim 为假（代码符号）：按行拼接，代码切点本来就是行/结构边界。
func assembleContextText(rows []entity.KnowledgeChunkText, hit Hit, budget int, trim bool) string {
	if len(rows) == 0 {
		return ""
	}
	selected := selectWindow(rows, hit.ChunkIndex, budget)

	var builder strings.Builder
	previous := ""
	for index, row := range selected {
		content := row.Content
		if index > 0 {
			separator := "\n\n"
			if trim {
				if trimmed, ok := trimOverlap(previous, content); ok {
					content = trimmed
					separator = ""
				}
			} else {
				separator = "\n"
			}
			builder.WriteString(separator)
		}
		builder.WriteString(content)
		previous = row.Content
	}

	text := strings.TrimSpace(builder.String())
	if text == strings.TrimSpace(hit.Content) {
		// 单块组（或窗口里只有命中片）：没有额外信息，不重复交付。
		return ""
	}
	return text
}

// selectWindow 选要拼的切片：整组放得下就全要；放不下就以命中片为中心向两侧扩展。
//
// 长节截断时**不能从头截**：命中片可能在中后段，截掉它等于没给上下文。这里从命中片
// 出发、先右后左交替纳入，两侧都放不下就停；选择结果保持原顺序（升序），拼接不必再排。
func selectWindow(rows []entity.KnowledgeChunkText, anchor int32, budget int) []entity.KnowledgeChunkText {
	total := 0
	for _, row := range rows {
		total += utf8.RuneCountInString(row.Content)
	}
	if total <= budget {
		return rows
	}

	anchorIndex := 0
	for index, row := range rows {
		if row.ChunkIndex <= anchor {
			anchorIndex = index
		}
	}

	selected := make([]bool, len(rows))
	selected[anchorIndex] = true
	length := utf8.RuneCountInString(rows[anchorIndex].Content)

	for left, right := anchorIndex-1, anchorIndex+1; ; {
		advanced := false
		if right < len(rows) {
			size := utf8.RuneCountInString(rows[right].Content)
			if length+size <= budget {
				selected[right] = true
				length += size
				right++
				advanced = true
			}
		}
		if left >= 0 {
			size := utf8.RuneCountInString(rows[left].Content)
			if length+size <= budget {
				selected[left] = true
				length += size
				left--
				advanced = true
			}
		}
		if !advanced {
			break
		}
	}

	out := make([]entity.KnowledgeChunkText, 0, len(rows))
	for index, row := range rows {
		if selected[index] {
			out = append(out, row)
		}
	}
	return out
}

// trimOverlap 从 next 的开头裁掉它与 previous 结尾重叠的那一段。
//
// 切分器给相邻切片补重叠时有个不变式：next 的开头 = previous 结尾的一段后缀，
// 且这段后缀在 previous 里紧跟在一个句末标点之后（overlapSeed 的起点对齐到句首）。
// 所以这里从长到短找 next 的最长前缀，要求同时满足：它是 previous 的后缀，
// 且它在 previous 里的起点之前是句末标点（或正好是开头）。
//
// 找不到就原样返回：宁可留一点重复，也不能猜错边界把正文裁掉。返回的第二个值
// 表示是否真的裁了 —— 调用方据此决定拼接处要不要加空行。
func trimOverlap(previous, next string) (string, bool) {
	prev := []rune(strings.TrimSpace(previous))
	nextRunes := []rune(next)

	limit := maxTrimRunes
	if len(nextRunes) < limit {
		limit = len(nextRunes)
	}
	if len(prev) < limit {
		limit = len(prev)
	}

	for size := limit; size >= trimOverlapMinRunes; size-- {
		start := len(prev) - size
		if start > 0 && !isSentenceEnd(prev[start-1]) {
			continue
		}
		if string(prev[start:]) != string(nextRunes[:size]) {
			continue
		}
		trimmed := strings.TrimLeft(string(nextRunes[size:]), "\n")
		if strings.TrimSpace(trimmed) == "" {
			// 整个 next 都是重叠（异常形态）：不裁，避免把一片切没了。
			return next, false
		}
		return trimmed, true
	}
	return next, false
}

// trimOverlapMinRunes 是最短可裁长度：一两个字符的"匹配"多半是巧合
// （"了。""的"这类），裁掉的风险大于收益。正常重叠是整句起步，远大于它。
const trimOverlapMinRunes = 4
