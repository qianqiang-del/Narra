package repository

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

// knowledge_search_assembly_test.go 盯住上下文装配的三个只读查询：
// 按序号升序、按节/符号/窗口过滤，以及"只读可检索文档"的底线过滤。
// 与召回用例同一个理由跑真库：正确性全在 SQL 里。

// seedSearchChunkText 造一个装配查询形状的切片：section_path / symbol 传空串即 NULL。
func seedSearchChunkText(t *testing.T, tx *gorm.DB, documentID uint64, index int, sectionPath, symbol, content string) {
	t.Helper()

	var section any
	if sectionPath != "" {
		section = sectionPath
	}
	var symbolValue any
	if symbol != "" {
		symbolValue = symbol
	}
	err := tx.Exec(`
		INSERT INTO knowledge_chunks (created_at, updated_at, document_id, chunk_index, section_path, symbol, content, character_count)
		VALUES (now(), now(), ?, ?, ?, ?, ?, ?)`,
		documentID, index, section, symbolValue, content, len([]rune(content))).Error
	if err != nil {
		t.Fatalf("插入装配测试切片失败: %v", err)
	}
}

func TestListChunkTextsForAssembly(t *testing.T) {
	tx := testTx(t)
	ready := seedSearchDocument(t, tx, "装配测试", entity.KnowledgeDocumentStatusReady, true)
	disabled := seedSearchDocument(t, tx, "已停用", entity.KnowledgeDocumentStatusReady, false)
	processing := seedSearchDocument(t, tx, "处理中", entity.KnowledgeDocumentStatusProcessing, true)

	seedSearchChunkText(t, tx, ready, 0, "第一章", "", "第一节第一片。")
	seedSearchChunkText(t, tx, ready, 1, "第一章", "", "第一节第二片。")
	seedSearchChunkText(t, tx, ready, 2, "第二章", "", "第二节第一片。")
	seedSearchChunkText(t, tx, ready, 3, "", "Parse", "func Parse() {")
	seedSearchChunkText(t, tx, ready, 4, "", "Parse", "}")
	seedSearchChunkText(t, tx, ready, 5, "", "", "无组文本。")

	// 底线：停用与处理中的文档不该被装配读到，即便节名一模一样。
	seedSearchChunkText(t, tx, disabled, 0, "第一章", "", "停用文档的片。")
	seedSearchChunkText(t, tx, processing, 0, "第一章", "", "处理中文档的片。")

	repo := NewKnowledgeSearchRepository(tx)
	ctx := context.Background()

	sections, err := repo.ListSectionChunkTexts(ctx, ready, "第一章")
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 2 || sections[0].Content != "第一节第一片。" || sections[1].Content != "第一节第二片。" {
		t.Fatalf("按节取切片不对: %+v", sections)
	}

	symbols, err := repo.ListSymbolChunkTexts(ctx, ready, "Parse")
	if err != nil {
		t.Fatal(err)
	}
	if len(symbols) != 2 || symbols[0].ChunkIndex != 3 || symbols[1].ChunkIndex != 4 {
		t.Fatalf("按符号取切片不对: %+v", symbols)
	}

	window, err := repo.ListChunkTextWindow(ctx, ready, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(window) != 2 || window[0].ChunkIndex != 2 || window[1].ChunkIndex != 3 {
		t.Fatalf("按窗口取切片不对: %+v", window)
	}

	if rows, err := repo.ListSectionChunkTexts(ctx, disabled, "第一章"); err != nil || len(rows) != 0 {
		t.Fatalf("停用文档不该被装配读到: rows=%+v err=%v", rows, err)
	}
	if rows, err := repo.ListSectionChunkTexts(ctx, processing, "第一章"); err != nil || len(rows) != 0 {
		t.Fatalf("处理中文档不该被装配读到: rows=%+v err=%v", rows, err)
	}
}
