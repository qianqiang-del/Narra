// lexical_index.go：词法检索的表达式索引维护（pg_bigm 优先，pg_trgm 兜底）。
//
// 为什么需要它：词法路是 ILIKE 子串匹配，没有索引就是全表顺序扫描 —— 数据量一大，
// 检索会静默变慢（不报错、不失败，只是慢）。这与向量索引是同一种问题，处理方式也相同：
// 索引由服务自己补齐，而不是留一段需要人工执行的 SQL。
//
// 匹配面只有"切片正文 + 章节标题"：文档标题在另一张表，跨表表达式建不了索引，
// 所以标题由 SearchLexical 的独立小查询覆盖，合并候选时取两条路的较高分。
//
// 两种扩展的取舍：
//
//   - pg_bigm：2-gram，专为 CJK 设计 —— 词典词大多是两字，选择度最好，首选；
//   - pg_trgm：3-gram，官方 contrib、几乎到处都有，作为兜底。注意它对**两字中文**
//     基本无效：两字的 trigram 全是首尾填充项，剪不掉多少行（5 万行实测仍走顺序扫描），
//     只有三字以上中文与拉丁标识能用上索引。所以 trgm 环境只是部分提速，
//     两字词的完整提速要等部署环境能装 pg_bigm。
//
// 两者对 ILIKE 的用法完全一致，检索 SQL 不感知用了哪一种 —— 本文件只影响索引 DDL。
// 一个都装不上时搜索退化为顺序扫描：结果不受影响，只是会慢。
package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// ErrLexicalIndexUnavailable 表示数据库上没有可用的中文子串索引扩展
// （pg_bigm 与 pg_trgm 都没有）。
//
// 与"建索引失败"分开对待：这是**环境属性**，检索照常正确（顺序扫描），只是会慢，
// 所以调用方应当提示而不是告警，更不该让它打断启动。
var ErrLexicalIndexUnavailable = errors.New("没有可用的中文子串索引扩展")

// lexicalIndexName 是词法表达式索引的名字。全库一条：匹配面与模型、维度都无关。
const lexicalIndexName = "knowledge_chunks_lexical_idx"

// lexicalIndexExpression 是索引定义的表达式：切片正文 + 章节标题。
//
// 检索 SQL 里的同义表达式带上表别名（规划器按解析后的表达式树匹配，别名不影响；
// 两者的等价性由 EXPLAIN 用例钉住，见 TestSearchLexicalCanUseIndex）：
//
//	(c.content || ' ' || coalesce(c.heading, ''))
const lexicalIndexExpression = `(content || ' ' || coalesce(heading, ''))`

// lexicalIndexKind 是词法索引所用的扩展。
type lexicalIndexKind string

const (
	lexicalIndexKindBigm lexicalIndexKind = "pg_bigm"
	lexicalIndexKindTrgm lexicalIndexKind = "pg_trgm"
)

// opclass 返回该扩展的 GIN 算子类名。装的是哪个扩展，索引就必须用哪个算子类。
func (k lexicalIndexKind) opclass() string {
	if k == lexicalIndexKindBigm {
		return "gin_bigm_ops"
	}
	return "gin_trgm_ops"
}

// EnsureLexicalIndex 补建词法表达式索引，幂等，返回实际使用的扩展。
//
// 与 EnsureVectorIndex 同一套约束：
//
//   - CONCURRENTLY 建索引，收录写入照常；因此**不能在事务里调用**；
//   - 同名索引存在但无效、或用的是另一个扩展的算子类时，删掉重建 ——
//     前者规划器不会用，后者查询永远匹配不上，而且都会占着名字让 IF NOT EXISTS 跳过；
//   - 失败只影响速度，不影响检索正确性，调用方按告警处理（见 app.initDatabase）。
func EnsureLexicalIndex(ctx context.Context, db *gorm.DB) (lexicalIndexKind, error) {
	kind, err := pickLexicalIndexKind(ctx, db)
	if err != nil {
		return "", err
	}
	if err := db.WithContext(ctx).Exec("CREATE EXTENSION IF NOT EXISTS " + string(kind)).Error; err != nil {
		return "", fmt.Errorf("创建扩展 %s 失败: %w", kind, err)
	}

	definition, valid, err := currentLexicalIndex(ctx, db)
	if err != nil {
		return "", err
	}
	switch lexicalIndexDisposition(definition, valid, kind.opclass()) {
	case indexActionKeep:
		return kind, nil
	case indexActionRebuild:
		if err := db.WithContext(ctx).Exec("DROP INDEX CONCURRENTLY IF EXISTS " + lexicalIndexName).Error; err != nil {
			return "", fmt.Errorf("删除词法索引 %s 失败: %w", lexicalIndexName, err)
		}
	}

	statement := fmt.Sprintf(
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS %s ON knowledge_chunks USING gin (%s %s)",
		lexicalIndexName, lexicalIndexExpression, kind.opclass(),
	)
	if err := db.WithContext(ctx).Exec(statement).Error; err != nil {
		return "", fmt.Errorf("创建词法索引 %s 失败: %w", lexicalIndexName, err)
	}
	return kind, nil
}

// pickLexicalIndexKind 在数据库能装的扩展里选一个：pg_bigm 优先，pg_trgm 兜底。
//
// 查 pg_available_extensions 而不是 pg_extension：前者表示"二进制装着、能建"，
// 后者只表示"当前库建过"。两个都装不了时返回 ErrLexicalIndexUnavailable。
func pickLexicalIndexKind(ctx context.Context, db *gorm.DB) (lexicalIndexKind, error) {
	var names []string
	err := db.WithContext(ctx).Raw(
		`SELECT name FROM pg_available_extensions WHERE name IN ('pg_bigm', 'pg_trgm')`,
	).Scan(&names).Error
	if err != nil {
		return "", fmt.Errorf("探测中文索引扩展失败: %w", err)
	}
	for _, candidate := range []lexicalIndexKind{lexicalIndexKindBigm, lexicalIndexKindTrgm} {
		for _, name := range names {
			if name == string(candidate) {
				return candidate, nil
			}
		}
	}
	return "", ErrLexicalIndexUnavailable
}

// lexicalIndexDisposition 判断同名词法索引该怎么处置（复用索引维护的三态）。
//
// 与向量索引同一条道理：无效的索引规划器永远不用；算子类对不上的索引（部署换过扩展）
// 用不上，也必须删掉重建。
func lexicalIndexDisposition(definition string, valid bool, opclass string) indexAction {
	if strings.TrimSpace(definition) == "" {
		return indexActionCreate
	}
	if valid && strings.Contains(definition, opclass) {
		return indexActionKeep
	}
	return indexActionRebuild
}

// currentLexicalIndex 取同名索引的定义与有效性；索引不存在时是空串与 false。
func currentLexicalIndex(ctx context.Context, db *gorm.DB) (string, bool, error) {
	var row struct {
		Definition string
		Valid      bool
	}
	err := db.WithContext(ctx).Raw(`
SELECT pg_get_indexdef(i.indexrelid) AS definition, i.indisvalid AS valid
FROM pg_index i
JOIN pg_class c ON c.oid = i.indexrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relname = ? AND n.nspname = current_schema()`, lexicalIndexName).Scan(&row).Error
	if err != nil {
		return "", false, fmt.Errorf("查询词法索引 %s 的定义失败: %w", lexicalIndexName, err)
	}
	return row.Definition, row.Valid, nil
}
