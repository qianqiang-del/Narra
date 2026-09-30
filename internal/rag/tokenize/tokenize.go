// tokenize.go 词法路的分词层：把一段检索文本切成词项（参与召回）与短语（参与打分）。
//
// 为什么切词在应用侧、而不是交给数据库（zhparser / pg_jieba）：词的权威必须与代码
// 同生共死 —— 词典随仓库发版，分词行为能在离线用例里钉住，加词不用连数据库、更不用
// 重建索引；数据库只负责"快速找到含这个词的切片"。这一层是纯函数（词典注入），
// 不碰 IO，符合检索链路"完全离线可测"的装法。
//
// 切分形态由三段组成：
//
//  1. 非 CJK（字母、数字、编号）整段作词项 —— "W38"、"P99"、"pgvector" 这类精确词
//     是向量路最容易漏、词法路最该保住的形态，绝不能拆开；
//  2. CJK 走 gse 词典分词（纯 Go、词典内嵌），词典不认识的连续汉字退成相邻二元组 ——
//     OOV 不丢召回，代价是"量检"这种噪声对，权重给得最低；
//  3. 检索词里的连续"词字符"段原样留一份作短语，只用于打分加权重 —— 词典分词会把
//     "向量检索"切成两个词，短语把"整段原样出现"这个更强的信号补回来。
//
// 输出是确定性的：同一输入永远得到同样的词项与顺序，调参和排障才可复算。
package tokenize

import (
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/go-ego/gse"
)

// 词项权重的档位。两条召回路最终交给 RRF 融合，融合只看名次，所以这里给的是
// "相对含金量"，不是绝对得分 —— 命中一个精确词，比命中好几个兜底二元组更能说明相关。
const (
	// WeightExact 精确词（拉丁字母、数字、编号）：命中就是真命中，几乎不会撞车。
	WeightExact = 3.0

	// WeightDict 词典词：词典认可的中文词，是词，但比较常见。
	WeightDict = 2.0

	// WeightBigram 二元组兜底：词典不认识的连续汉字拆出的相邻对，可能是"量检"这种噪声。
	WeightBigram = 1.0

	// WeightPhrase 短语：检索词里的整段原样出现，是最强的精度信号。
	WeightPhrase = 4.0
)

const (
	// DefaultMaxTerms 是词项数的默认上限：给 SQL 兜底，词项越多每次检索扫得越久。
	DefaultMaxTerms = 8

	// DefaultMaxPhrases 是短语数的默认上限：短语只加分，多留无益。
	DefaultMaxPhrases = 4

	// maxPhraseRunes 是短语的最大字符数：整句自然语言拿去做子串匹配几乎不可能命中，
	// 留着只会白占一个 SQL 参数。
	maxPhraseRunes = 12

	// userWordFreq 是自定义词典词的词频：给得比普通词高，短路优先切出整个词，
	// 否则"向量检索"会被自带词典按更碎的方式切掉，加词就等于没加。
	userWordFreq = 1000.0
)

// Term 是一个带权词项。
type Term struct {
	Text   string
	Weight float64
}

// Result 是一次切分的产物。
type Result struct {
	// Terms 参与召回：命中任意一个都可能入选，按权重与出现顺序截断。
	Terms []Term

	// Phrases 只参与打分：在命中词项的行上，整段原样出现额外加分。
	Phrases []Term
}

// Options 是构造分词器时的口径。
type Options struct {
	// MaxTerms / MaxPhrases 是词项与短语的上限；≤0 时用 Default 值。
	MaxTerms   int
	MaxPhrases int

	// UserWords 是自定义词典词（产品名、内部代号等）：在自带词典之上追加。
	UserWords []string
}

// Tokenizer 是带词典的分词器。
type Tokenizer struct {
	seg *gse.Segmenter

	// mu 保护 seg：gse 内部没有任何并发保护，而分词器是进程级单例、被并发检索共用。
	// 切一句查询是微秒级操作，串行化的代价远小于给每个请求各建一份词典。
	mu sync.Mutex

	maxTerms   int
	maxPhrases int
}

// New 加载内嵌词典并追加自定义词，返回可复用的分词器。
//
// 词典用简体 + 繁体两份：繁体资料与查询走同一份词典，切不出来时还有二元组兜底，
// 不会因为缺词典而整段丢召回。
func New(opts Options) (*Tokenizer, error) {
	seg, err := gse.NewEmbed("zh")
	if err != nil {
		return nil, err
	}

	added := false
	for _, word := range opts.UserWords {
		word = strings.TrimSpace(word)
		if word == "" {
			continue
		}
		if err := seg.AddToken(word, userWordFreq); err != nil {
			return nil, err
		}
		added = true
	}
	// CalcToken 是 O(词典) 的，只在真的加过词时算一次，而不是每加一个词算一次。
	if added {
		seg.CalcToken()
	}

	return &Tokenizer{
		seg:        &seg,
		maxTerms:   positiveOr(opts.MaxTerms, DefaultMaxTerms),
		maxPhrases: positiveOr(opts.MaxPhrases, DefaultMaxPhrases),
	}, nil
}

// Analyse 切分一段**已归一化**的检索文本。调用方负责宽度、空白与剥壳（见 query.go），
// 本函数只做切词。
func (t *Tokenizer) Analyse(text string) Result {
	t.mu.Lock()
	defer t.mu.Unlock()

	var (
		exact   []Term
		words   []Term
		bigrams []Term
	)
	appendTerm := termAppender()

	for _, run := range splitRuns(text) {
		if !run.cjk {
			// 长度 1 的拉丁词（"a"）命中半张表，丢。
			if utf8.RuneCountInString(run.text) >= 2 {
				appendTerm(&exact, run.text, WeightExact)
			}
			continue
		}

		// 词典切分（不用 HMM）：不认识的连续汉字会落成一个个单字，把它们攒起来
		// 统一做二元组兜底。HMM 猜出来的"词"不在词典里、也匹配不到正文里的词，
		// 只会挤占名额 —— 兜底的职责交给二元组，那里至少有字面依据。
		var unknown []rune
		flushUnknown := func() {
			if len(unknown) >= 2 {
				for index := 0; index+1 < len(unknown); index++ {
					appendTerm(&bigrams, string(unknown[index:index+2]), WeightBigram)
				}
			}
			// 单字（"的""了"）与空段都丢。
			unknown = unknown[:0]
		}

		for _, piece := range t.seg.Cut(run.text, false) {
			if utf8.RuneCountInString(piece) >= 2 {
				flushUnknown()
				appendTerm(&words, piece, WeightDict)
				continue
			}
			unknown = append(unknown, []rune(piece)...)
		}
		flushUnknown()
	}

	terms := takeTerms(t.maxTerms, exact, words, bigrams)
	return Result{Terms: terms, Phrases: extractPhrases(text, terms, t.maxPhrases)}
}

// RuleBased 是不依赖词典的降级切分：非 CJK 整段、CJK 二字整段、更长的拆相邻二元组、
// 单字丢弃。口径与改造前的规则完全一致 —— 分词器初始化失败（词典加载不了）时，
// 词法路照常可用，只是切得糙一点。
func RuleBased(text string, maxTerms, maxPhrases int) Result {
	var (
		words   []Term
		bigrams []Term
	)
	appendTerm := termAppender()

	for _, run := range splitRuns(text) {
		runes := []rune(run.text)
		if !run.cjk {
			if len(runes) >= 2 {
				appendTerm(&words, run.text, WeightExact)
			}
			continue
		}
		switch {
		case len(runes) < 2:
			// 单字噪声，丢。
		case len(runes) == 2:
			appendTerm(&words, run.text, WeightDict)
		default:
			for index := 0; index+1 < len(runes); index++ {
				appendTerm(&bigrams, string(runes[index:index+2]), WeightBigram)
			}
		}
	}

	terms := takeTerms(positiveOr(maxTerms, DefaultMaxTerms), words, bigrams)
	return Result{Terms: terms, Phrases: extractPhrases(text, terms, positiveOr(maxPhrases, DefaultMaxPhrases))}
}

// termAppender 返回一个按"大小写不敏感去重"往桶里追加词项的函数。
// 同一个词重复出现只该值一次分，留两份只会白占名额。
func termAppender() func(bucket *[]Term, text string, weight float64) {
	seen := make(map[string]struct{})
	return func(bucket *[]Term, text string, weight float64) {
		key := strings.ToLower(text)
		if _, duplicated := seen[key]; duplicated {
			return
		}
		seen[key] = struct{}{}
		*bucket = append(*bucket, Term{Text: text, Weight: weight})
	}
}

// takeTerms 按"精确词 → 词典词 → 二元组"的顺序合并并截断到 max 个。
// 名额不够时先保精确词：专有名词与编号挑得动结果，兜底二元组之间却彼此可以替代。
func takeTerms(max int, buckets ...[]Term) []Term {
	terms := make([]Term, 0, max)
	for _, bucket := range buckets {
		for _, term := range bucket {
			if len(terms) >= max {
				return terms
			}
			terms = append(terms, term)
		}
	}
	return terms
}

// extractPhrases 从原文里提取短语：连续的"词字符"段（不分中英）长度在
// [2, maxPhraseRunes] 之内，且没有和已选词项重复。
//
// 与词项重复的短语不加：那说明整个短语本来就是一个词项（"索引"），
// 再加一份只是把同一个信号算两遍。
func extractPhrases(text string, terms []Term, max int) []Term {
	if max <= 0 {
		return nil
	}

	termSet := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		termSet[strings.ToLower(term.Text)] = struct{}{}
	}

	var phrases []Term
	seen := make(map[string]struct{}, max)
	for _, span := range splitWordSpans(text) {
		length := utf8.RuneCountInString(span)
		if length < 2 || length > maxPhraseRunes {
			continue
		}
		key := strings.ToLower(span)
		if _, isTerm := termSet[key]; isTerm {
			continue
		}
		if _, duplicated := seen[key]; duplicated {
			continue
		}
		seen[key] = struct{}{}
		phrases = append(phrases, Term{Text: span, Weight: WeightPhrase})
		if len(phrases) >= max {
			break
		}
	}
	return phrases
}

// lexicalRun 是文本里一段连续的词字符，以及它是不是 CJK 段。
type lexicalRun struct {
	text string
	cjk  bool
}

// splitRuns 按字符类别把文本切成若干段：段内字符同为 CJK 或同为非 CJK，
// 空白与标点都是分隔。中英混排（"pgvector索引"）在这里被切成两段。
func splitRuns(text string) []lexicalRun {
	var (
		runs    []lexicalRun
		current []rune
	)
	currentCJK := false

	flush := func() {
		if len(current) > 0 {
			runs = append(runs, lexicalRun{text: string(current), cjk: currentCJK})
			current = current[:0]
		}
	}

	for _, character := range text {
		if !isWordRune(character) {
			flush()
			continue
		}
		cjk := isCJKRune(character)
		if len(current) > 0 && cjk != currentCJK {
			flush()
		}
		currentCJK = cjk
		current = append(current, character)
	}
	flush()
	return runs
}

// splitWordSpans 把文本切成"连续词字符"段（不分中英），用于提取短语。
// 与 splitRuns 的差别：这里不在中英边界切，于是 "pgvector索引" 整段保留。
func splitWordSpans(text string) []string {
	var (
		spans   []string
		current []rune
	)
	for _, character := range text {
		if !isWordRune(character) {
			if len(current) > 0 {
				spans = append(spans, string(current))
				current = current[:0]
			}
			continue
		}
		current = append(current, character)
	}
	if len(current) > 0 {
		spans = append(spans, string(current))
	}
	return spans
}

// isWordRune 判断一个字符能不能待在词项中间。
//
// 用"字母或数字"而不是"不是标点"：后者会把 ①、★、→ 这类符号也收进词项，
// 它们既匹配不到东西，又会把词项的字面量撑长。
func isWordRune(character rune) bool {
	return unicode.IsLetter(character) || unicode.IsDigit(character)
}

// isCJKRune 判断一个字符是不是中日韩文字。
//
// 日文假名与韩文一并算进来：它们与中文一样没有空格分词，二元组的近似同样适用。
// 标点（，。！？）虽然在这些区段里，但已经被 isWordRune 挡在外面了。
func isCJKRune(character rune) bool {
	return unicode.Is(unicode.Han, character) ||
		unicode.Is(unicode.Hiragana, character) ||
		unicode.Is(unicode.Katakana, character) ||
		unicode.Is(unicode.Hangul, character)
}

// positiveOr 返回 value（正数），否则返回 fallback。
func positiveOr(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
