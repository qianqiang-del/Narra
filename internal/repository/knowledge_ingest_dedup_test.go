package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"narra/internal/model/entity"
	"narra/internal/ownership"
	"narra/internal/rag"
	"narra/pkg/objectstorage"
)

// 两次提交需要独立提交事务，因此用独立 schema 运行实际 AutoMigrate。
func openKnowledgeIngestDedupDB(t *testing.T) *gorm.DB {
	t.Helper()

	base := openTestDB(t)
	basePool, err := base.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = basePool.Close() })

	schema := fmt.Sprintf("narra_test_ingest_%d", time.Now().UnixNano())
	if err := base.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatalf("创建测试 schema: %v", err)
	}
	t.Cleanup(func() {
		if err := base.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Errorf("清理测试 schema: %v", err)
		}
	})

	cfg, err := pgx.ParseConfig(os.Getenv("NARRA_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	pool := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = pool.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{
		Logger:                 gormlogger.Default.LogMode(gormlogger.Silent),
		SkipDefaultTransaction: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&entity.User{}, &entity.KnowledgeDocument{}, &entity.KnowledgeUploadRecord{}); err != nil {
		t.Fatalf("迁移测试表: %v", err)
	}
	return db
}

// 让两个请求都查完现有文档后再进入写库事务，确保走到数据库的唯一性约束。
type ingestDedupObjectStore struct {
	objectstorage.Client
	ctx      context.Context
	proceed  <-chan struct{}
	uploaded chan string
	deleted  chan string
}

func (s *ingestDedupObjectStore) PutFile(key, _ string) error {
	s.uploaded <- key
	select {
	case <-s.proceed:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *ingestDedupObjectStore) Delete(key string) error {
	s.deleted <- key
	return nil
}

func TestKnowledgeIngestDeduplicatesOriginalFiles(t *testing.T) {
	db := openKnowledgeIngestDedupDB(t)
	documents := NewKnowledgeDocumentRepository(db)
	records := NewKnowledgeUploadRecordRepository(db)
	tx := NewTransactionManager(db)
	ownerID := testOwnerID(t, db)
	otherOwnerID := testOwnerID(t, db)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	var submissions sync.WaitGroup
	defer func() {
		cancel()
		submissions.Wait()
	}()
	ownerCtx := ownership.WithOwner(ctx, ownerID)
	input := func(name, content string) rag.FileInput {
		t.Helper()
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(content))
		return rag.FileInput{
			Path:       path,
			SourceURI:  name,
			SizeBytes:  int64(len(content)),
			FileSHA256: hex.EncodeToString(digest[:]),
		}
	}
	const original = "# 研发文档\n"
	first := input("first.md", original)
	second := input("second.md", original)

	proceed := make(chan struct{})
	objects := &ingestDedupObjectStore{
		ctx:      ctx,
		proceed:  proceed,
		uploaded: make(chan string, 2),
		deleted:  make(chan string, 2),
	}
	ingester := rag.NewIngester(documents, records, nil, nil, nil, rag.IngestOptions{Tx: tx, Files: objects})
	type outcome struct {
		document *entity.KnowledgeDocument
		err      error
	}
	outcomes := make(chan outcome, 2)
	for _, file := range []rag.FileInput{first, second} {
		submissions.Go(func() {
			result, err := ingester.SubmitFile(ownerCtx, file)
			outcomes <- outcome{document: result.Document, err: err}
		})
	}
	keys := make(map[string]bool, 2)
	for range 2 {
		select {
		case key := <-objects.uploaded:
			keys[key] = true
		case result := <-outcomes:
			t.Fatalf("提交在写入原件前提前返回: %v", result.err)
		case <-ctx.Done():
			t.Fatal("等待两个并发上传超时")
		}
	}
	close(proceed)
	if len(keys) != 2 {
		t.Fatalf("两次上传应使用不同的 OSS key: %v", keys)
	}

	var winner *entity.KnowledgeDocument
	var duplicates int
	for range 2 {
		select {
		case result := <-outcomes:
			switch {
			case result.err == nil:
				winner = result.document
			case errors.Is(result.err, rag.ErrDuplicateKnowledgeFile):
				duplicates++
			default:
				t.Fatalf("并发提交返回意外错误: %v", result.err)
			}
		case <-ctx.Done():
			t.Fatal("等待并发提交完成超时")
		}
	}
	if duplicates != 1 || winner == nil {
		t.Fatalf("期望一个重复结果和一个成功结果，实际重复数 %d，文档 %v", duplicates, winner)
	}
	if count := countRows(t, db, &entity.KnowledgeDocument{}, "owner_id = ?", ownerID); count != 1 {
		t.Errorf("并发结束后文档数 = %d，期望 1", count)
	}
	if count := countRows(t, db, &entity.KnowledgeUploadRecord{}, "owner_id = ?", ownerID); count != 1 {
		t.Errorf("并发结束后上传记录数 = %d，期望 1", count)
	}
	stored, err := documents.GetByID(ownerCtx, winner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.FileSHA256 == nil || *stored.FileSHA256 != first.FileSHA256 {
		t.Fatal("成功提交应保存原始文件 SHA-256")
	}
	var metadata struct {
		UploadKey string `json:"upload_key"`
	}
	if err := json.Unmarshal(stored.Metadata, &metadata); err != nil {
		t.Fatal(err)
	}
	if !keys[metadata.UploadKey] || len(objects.deleted) != 1 {
		t.Fatalf("期望保留成功对象，仅删除重复对象一次，保留 %q，删除次数 %d", metadata.UploadKey, len(objects.deleted))
	}
	if key := <-objects.deleted; !keys[key] || key == metadata.UploadKey {
		t.Errorf("删除了错误的 OSS 对象 %q", key)
	}

	local := rag.NewIngester(documents, records, nil, nil, nil, rag.IngestOptions{Tx: tx})
	renamed := input("renamed.md", original)
	if _, err := local.SubmitFile(ownerCtx, renamed); !errors.Is(err, rag.ErrDuplicateKnowledgeFile) || !strings.Contains(err.Error(), "正在收录") {
		t.Errorf("同账号同内容改名应说明正在收录，实际错误 %v", err)
	}
	if _, err := local.SubmitFile(ownership.WithOwner(ctx, otherOwnerID), input("renamed.md", original)); err != nil {
		t.Errorf("不同账号可以上传相同内容: %v", err)
	}
	if _, err := local.SubmitFile(ownerCtx, input(winner.Title, "# 另一份文档\n")); err != nil {
		t.Errorf("同名不同内容可以上传: %v", err)
	}
	for range 2 {
		material := input("renamed.md", original)
		material.Purpose = entity.KnowledgeDocumentKindMaterial
		result, err := local.SubmitFile(ownerCtx, material)
		if err != nil || result.Document == nil || result.Document.Kind != entity.KnowledgeDocumentKindMaterial {
			t.Fatalf("课程材料应保持独立收录，文档 %v，错误 %v", result.Document, err)
		}
	}

	attempt, claimed, err := documents.ClaimAndReturnAttempt(ownerCtx, winner.ID)
	if err != nil || !claimed {
		t.Fatalf("认领原收录任务失败: %v", err)
	}
	failed, err := documents.MarkFailed(ownerCtx, winner.ID, attempt, nil, "测试收录失败")
	if err != nil || !failed {
		t.Fatalf("标记原收录任务失败: %v", err)
	}
	if _, err := local.SubmitFile(ownerCtx, renamed); !errors.Is(err, rag.ErrDuplicateKnowledgeFile) || !strings.Contains(err.Error(), "上次收录失败") {
		t.Errorf("重复上传失败文档应提示重试，实际错误 %v", err)
	}
	if err := documents.Delete(ownerCtx, winner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := local.SubmitFile(ownerCtx, renamed); err != nil {
		t.Errorf("删除原文档后应允许重新上传: %v", err)
	}
}
