package rag

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// query.go 检索前的确定性清洗：把调用方送来的检索词统一成"好检索"的形态。
//
// 为什么要有这一层：知识库检索有两个入口 —— Eino 工具 rag_retrieve（检索词由大模型写）
// 与 HTTP 接口（调用方直接给）。调用方可能把用户的整句话、甚至整段需求复制进来，也可能
// 带上全角字母数字。词法路的名额有限（maxLexicalTerms）且按出现顺序取词，"帮我找下之前
// 上传的……"这类外壳会把名额吃光，真正的实体词反而被挤出候选窗口。
//
// 这一层只做**减法**：统一宽度与空白、剥掉请求/指代/疑问外壳。它不猜意图、不改写内容，
// 也不引入任何模型或外部依赖 —— 那是调用方（Agent 提示词）的事；它保证的是接口对输入
// 宽容：无论谁来调、写得多随意，取到的词都干净一点。同样的输入永远得到同样的输出。

// queryPlan 是一次检索对检索词的加工结果。
type queryPlan struct {
	// EmbedText 给向量路：归一化并剥壳后的文本。
	//
	// 清洗只去包装、不改语义，所以两条路共用一个文本，不必各留一份；将来如果要做
	// 查询扩展（同义变体、多路召回），变体由调用方传入，也不在这层混进来。
	EmbedText string

	// Terms 给词法路：从 EmbedText 切出的词项，切分规则见 retrieve.go 的 lexicalTerms。
	Terms []string
}

// buildQueryPlan 把原始检索词加工成两条路各自的输入。
//
// 剥壳结果为空时回退到归一化后的原话：清洗层只允许把检索词变干净，绝不允许把它弄丢 ——
// "帮我找下"这种全是壳的输入，宁可拿原话去搜，也不能变成空查询。
func buildQueryPlan(raw string) queryPlan {
	normalized := normalizeQuery(raw)
	content := stripRequestShell(normalized)
	if strings.TrimSpace(content) == "" {
		content = normalized
	}
	return queryPlan{EmbedText: content, Terms: lexicalTerms(content)}
}

// normalizeQuery 统一字符宽度与空白：
//
//  1. 全角 ASCII（U+FF01–U+FF5E）整体转半角：全角字母、数字与标点落回 ASCII，
//     "Ｗ38" 才可能匹配到正文里的 "W38"；
//  2. 空白折叠成单个半角空格：全角空格 U+3000 被 unicode.IsSpace 覆盖，首尾顺带去掉。
//
// 大小写不动：词法路的 ILIKE 本来就忽略大小写，改写它只会平白改变向量路的输入。
func normalizeQuery(raw string) string {
	mapped := strings.Map(func(r rune) rune {
		switch {
		case r == '\u3000':
			return ' '
		case r >= '\uFF01' && r <= '\uFF5E':
			return r - 0xFEE0
		default:
			return r
		}
	}, raw)
	return strings.Join(strings.Fields(mapped), " ")
}

// requestShellPhrases 是请求/指代/内容/疑问四类外壳短语。
//
// 顺序必须是"长词优先"（见 longestFirst）：短词先剥会把长词切碎 —— 例如先剥"上传的"，
// "之前上传的"就再也匹配不上，只会剩一个孤零零的"之前"。用例
// TestRequestShellPhrasesSortedLongestFirst 钉住这个不变量。
//
// 只收"剥掉后不影响意图"的词：语气词（吗/呢/吧）刻意不收，它们是词的一部分时
// 误伤成本太高（酒吧、网吧剥成"酒""网"，查询直接废掉），而它们出现在检索词末尾的概率
// 很低，不值得为它冒这个险。
var requestShellPhrases = longestFirst([]string{
	// 请求壳：客套与代劳语气
	"帮我找一下", "帮我查一下", "帮我看一下", "帮忙找一下", "帮我找下", "帮我查下",
	"帮我看下", "请帮我找", "请帮我查", "请帮我", "我想知道", "我想了解",
	"我要找", "麻烦帮我", "麻烦", "请问", "帮我", "帮忙", "找一下", "查一下",
	"看一下", "找下", "查下", "看下", "一下",
	// 指代壳：说的人知道指什么，检索器不知道
	"之前上传的", "上次上传的", "刚才上传的", "之前传的", "我上传的", "上传的",
	"前面提到的", "刚才说的", "那份", "那个", "这份", "这个", "那些", "这些",
	// 内容壳：把"在哪说的"这类定位话剥掉，只留要找的东西
	"里面讲的", "里面说的", "里讲的", "里说的", "提到的", "关于", "有关",
	// 疑问壳：疑问词只在提问时有意义，检索器要的是被问的对象
	"什么是", "是什么", "什么叫", "怎么样", "怎么做", "怎么用", "怎么",
	"如何", "为什么", "有哪些", "哪些", "有没有", "是否", "多少",
})

// shellTrimmers 是剥壳后要清掉的边缘标点：中英文常见的句读与成对符号。
//
// 只列标点、不列符号（# + @ _ - / 等）：C++、C#、W-38 这类标识里的符号是内容本身，
// TrimFunc 虽然只清两端，但 "C#" 的 # 就在末尾，仍会被误伤，所以干脆一个符号都不碰。
const shellTrimmers = "，。！？；：、（）【】「」『』《》〈〉“”‘’·…—～.,;:!?()[]{}<>\"'`"

// stripRequestShell 剥掉检索词里的客套与指代外壳，返回剩余内容。
//
// 替换一律用空格而不是空串：直接删掉会把两侧内容粘成一个不存在的词，空格能保住
// 分词边界。全部替换完再统一折叠空白、清掉悬挂标点。
//
// 结果可能为空（输入全是壳），是否回退由 buildQueryPlan 决定：本函数只负责"剥"。
func stripRequestShell(normalized string) string {
	result := normalized
	for _, phrase := range requestShellPhrases {
		result = strings.ReplaceAll(result, phrase, " ")
	}

	parts := strings.Fields(result)
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimFunc(part, isShellTrimmer); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	return strings.Join(kept, " ")
}

// isShellTrimmer 判断一个字符是不是剥壳后要清掉的边缘标点。
// 常量集只有几十个字符，ContainsRune 的线性扫描比建 map 更省（调用频率极低）。
func isShellTrimmer(r rune) bool {
	return strings.ContainsRune(shellTrimmers, r) || unicode.IsSpace(r)
}

// longestFirst 返回按字符数降序排列的副本，供剥壳按"长词优先"执行。
//
// 用字符数而不是字节数：中文一个字占 3 字节，按字节排会把 4 个汉字的词排到
// 8 个英文字母之前，长度关系就乱了。
func longestFirst(phrases []string) []string {
	out := slices.Clone(phrases)
	slices.SortStableFunc(out, func(a, b string) int {
		return cmp.Compare(utf8.RuneCountInString(b), utf8.RuneCountInString(a))
	})
	return out
}
