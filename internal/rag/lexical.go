// lexical.go 检索侧的词法加工：清洗后的文本 → 带权词项 + 短语（见 internal/rag/tokenize）。
//
// 分词器是进程级单例：词典加载是秒级动作，只做一次；初始化失败不致命 —— 退回不依赖
// 词典的二元组规则，词法路照常可用，只是切得糙一点。这条降级与"两路召回互不拖累"
// 是同一个立场：能查到的照常查，坏掉的那一小块留一条日志，不静默。
package rag

import (
	"sync"

	"go.uber.org/zap"

	"narra/internal/model/entity"
	"narra/internal/rag/tokenize"
	"narra/pkg/logger"
)

// maxLexicalPhrases 是词法路最多用几个短语。短语只参与打分，多留无益；
// 上限同时也是 SQL 参数数量的一层兜底。
const maxLexicalPhrases = 4

// lexicalPlan 是一次检索的词法输入。
type lexicalPlan struct {
	// Terms 参与召回：命中任意一个都可能进入候选窗口。
	Terms []entity.KnowledgeLexicalTerm

	// Phrases 只参与打分：命中词项的行上，整段原样出现额外加分。
	Phrases []entity.KnowledgeLexicalTerm
}

var (
	lexicalTokenizerOnce sync.Once
	lexicalTokenizer     *tokenize.Tokenizer
)

// WarmupLexicalTokenizer 预热分词器，供服务启动时调用 —— 词典加载有秒级开销，
// 挂在第一个检索请求上会让那一次请求凭空变慢。
func WarmupLexicalTokenizer() {
	defaultLexicalTokenizer()
}

// defaultLexicalTokenizer 返回进程级分词器；初始化失败时返回 nil（调用方走降级）。
func defaultLexicalTokenizer() *tokenize.Tokenizer {
	lexicalTokenizerOnce.Do(func() {
		tokenizer, err := tokenize.New(tokenize.Options{
			MaxTerms:   maxLexicalTerms,
			MaxPhrases: maxLexicalPhrases,
		})
		if err != nil {
			logger.Warn("中文分词器初始化失败，词法路退回不依赖词典的二元组规则", zap.Error(err))
			return
		}
		lexicalTokenizer = tokenizer
	})
	return lexicalTokenizer
}

// buildLexicalPlan 把清洗后的检索词切分给词法路。
func buildLexicalPlan(text string) lexicalPlan {
	var result tokenize.Result
	if tokenizer := defaultLexicalTokenizer(); tokenizer != nil {
		result = tokenizer.Analyse(text)
	} else {
		result = tokenize.RuleBased(text, maxLexicalTerms, maxLexicalPhrases)
	}
	return lexicalPlan{
		Terms:   toLexicalTerms(result.Terms),
		Phrases: toLexicalTerms(result.Phrases),
	}
}

// toLexicalTerms 把分词层的产物翻成对外的形状（entity 是两层共享的类型，不是表结构）。
func toLexicalTerms(terms []tokenize.Term) []entity.KnowledgeLexicalTerm {
	out := make([]entity.KnowledgeLexicalTerm, 0, len(terms))
	for _, term := range terms {
		out = append(out, entity.KnowledgeLexicalTerm{Text: term.Text, Weight: term.Weight})
	}
	return out
}

// lexicalTermTexts 取出词项文本，用于响应回显（terms 字段）与排障。
func lexicalTermTexts(terms []entity.KnowledgeLexicalTerm) []string {
	out := make([]string, 0, len(terms))
	for _, term := range terms {
		out = append(out, term.Text)
	}
	return out
}
