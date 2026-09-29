package rag

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// query_test.go 覆盖检索前的确定性清洗：宽度归一、空白折叠、剥壳与回退。
// 全是纯函数用例，不连数据库、不调向量服务。

// TestNormalizeQueryNormalizesWidthAndSpaces 校验全角字符落回 ASCII、空白折叠成单空格。
func TestNormalizeQueryNormalizesWidthAndSpaces(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"全角字母数字", "Ｗ38　和　８００ｍｓ", "W38 和 800ms"},
		{"全角标点", "ＡＢＣ，ＤＥＦ", "ABC,DEF"},
		{"空白折叠与首尾清理", "  向量\n\t检索  ", "向量 检索"},
		{"空串", "", ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := normalizeQuery(testCase.in); got != testCase.want {
				t.Fatalf("归一化结果不对: %q（期望 %q）", got, testCase.want)
			}
		})
	}
}

// TestStripRequestShellPeelsPhrases 校验客套、指代、疑问外壳被剥掉，内容与标识原样保留。
func TestStripRequestShellPeelsPhrases(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"口水话整句", "帮我找下之前上传的那份讲义里讲的令牌桶算法", "讲义 令牌桶算法"},
		{"疑问句", "什么是三色标记法", "三色标记法"},
		{"疑问句带标点", "怎么优化检索速度？", "优化检索速度"},
		{"全是壳", "帮我找下", ""},
		{"精确词不受影响", "P99 和 W38 报错", "P99 和 W38 报错"},
		{"英文不受影响", "how to optimize retrieval", "how to optimize retrieval"},
		// 语气词刻意不剥：它们是词的一部分时误伤成本太高（"酒吧"会被剥成"酒"），
		// 用一条用例把这个决定钉住，防止后来人"顺手"加上。
		{"语气词保留", "上海的酒吧", "上海的酒吧"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := stripRequestShell(testCase.in); got != testCase.want {
				t.Fatalf("剥壳结果不对: %q（期望 %q）", got, testCase.want)
			}
		})
	}
}

// TestQueryCleaningIsIdempotent 校验清洗是幂等的：同一段文本过两遍与过一遍结果一致 ——
// 链路将来若重试或复用清洗结果，"越洗越短"会变成很难查的偶发问题。
func TestQueryCleaningIsIdempotent(t *testing.T) {
	samples := []string{
		"帮我找下之前上传的那份讲义里讲的令牌桶算法",
		"什么是三色标记法",
		"帮我找下",
		"P99 和 W38 报错",
		"上海的酒吧",
	}
	for _, sample := range samples {
		normalized := normalizeQuery(sample)
		if twice := normalizeQuery(normalized); twice != normalized {
			t.Fatalf("归一化应当幂等: %q → %q → %q", sample, normalized, twice)
		}
		stripped := stripRequestShell(normalized)
		if twice := stripRequestShell(stripped); twice != stripped {
			t.Fatalf("剥壳应当幂等: %q → %q → %q", sample, stripped, twice)
		}
	}
}

// TestRequestShellPhrasesSortedLongestFirst 钉住"长词优先"这个不变量：
// 短词排在长词前面会把长词切碎（先剥"上传的"，"之前上传的"就再也匹配不上）。
// 往表里加词的人不必记得排序，用这个用例兜 —— 与向量索引那条 EXPLAIN 断言同一个路数。
func TestRequestShellPhrasesSortedLongestFirst(t *testing.T) {
	if len(requestShellPhrases) == 0 {
		t.Fatal("短语表不能为空")
	}
	for index, phrase := range requestShellPhrases {
		if phrase == "" || strings.TrimSpace(phrase) != phrase {
			t.Fatalf("短语 %q 是空串或前后带空白", phrase)
		}
		if index == 0 {
			continue
		}
		previous := requestShellPhrases[index-1]
		previousLength := utf8.RuneCountInString(previous)
		length := utf8.RuneCountInString(phrase)
		if previousLength < length {
			t.Fatalf("短语必须按字符数降序：%q(%d) 排在了 %q(%d) 前面",
				previous, previousLength, phrase, length)
		}
	}
}

// TestBuildQueryPlanPeelsShell 校验加工结果：两路共用一个剥壳后的文本，词项由它切出。
func TestBuildQueryPlanPeelsShell(t *testing.T) {
	plan := buildQueryPlan("帮我找下之前上传的那份讲义里讲的令牌桶算法")

	if plan.EmbedText != "讲义 令牌桶算法" {
		t.Fatalf("向量路文本应当是剥壳后的内容，实际 %q", plan.EmbedText)
	}
	if got := termTexts(plan.Terms); got != "讲义,令牌,算法" {
		t.Fatalf("词法路词项不对: %v", got)
	}
	if len(plan.Phrases) != 1 || plan.Phrases[0].Text != "令牌桶算法" {
		t.Fatalf("词法路短语不对: %+v", plan.Phrases)
	}
}

// TestBuildQueryPlanFallsBackWhenAllShell 校验全是外壳时回退归一化原话：
// 清洗层只允许把检索词变干净，绝不允许把它弄丢。
func TestBuildQueryPlanFallsBackWhenAllShell(t *testing.T) {
	plan := buildQueryPlan("帮我找下")

	if plan.EmbedText != "帮我找下" {
		t.Fatalf("全壳输入应当回退到归一化原话，实际 %q", plan.EmbedText)
	}
	if got := termTexts(plan.Terms); got != "帮我,我找,找下" {
		t.Fatalf("回退后的词项应当来自原话: %v", got)
	}
}

// TestBuildQueryPlanKeepsCleanQuery 校验干净查询原样通过：
// 清洗层的存在不能改变既有正常路径的行为，词项口径必须与 lexicalTerms 一致。
func TestBuildQueryPlanKeepsCleanQuery(t *testing.T) {
	const query = "P99 800ms knowledge_embeddings 索引"
	plan := buildQueryPlan(query)

	if plan.EmbedText != query {
		t.Fatalf("干净查询不该被改动: %q", plan.EmbedText)
	}
	if got := termTexts(plan.Terms); got != "P99,800ms,knowledge,embeddings,索引" {
		t.Fatalf("词项口径不对: %v", got)
	}
}
