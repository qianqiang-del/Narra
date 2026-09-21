package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// 这里只测 archiveStagedFile —— 它是"失败原件留下来"的实现处，而重试完全建立在
// 这个前提上：文件不在磁盘上，把文档改回 pending 也跑不出结果。
//
// 用真文件系统而不是替身：这一段的正确性全在文件系统语义里（目录改名、目标已存在
// 时先清、源不存在时安静返回），假的目录层次测不出来。
//
// 替身只承担"库里的 metadata 长什么样"：fakeDocumentStore 的 SetUploadPath
// 是按真仓储的合并语义写的（见 ingest_test.go），所以下面能顺带验到
// "归档时不会把失败现场抹掉"。

// stageFile 在暂存目录里造一份待处理的文件，返回它的路径。
func stageFile(t *testing.T, root, name, content string) string {
	t.Helper()

	directory := filepath.Join(root, "pending", name)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("创建暂存目录失败: %v", err)
	}
	path := filepath.Join(directory, "upload.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入暂存文件失败: %v", err)
	}
	return path
}

// archiveDir 返回某个文档的失败归档目录。
func archiveDir(root string, documentID uint64) string {
	return filepath.Join(root, failedDirName, strconv.FormatUint(documentID, 10))
}

func TestWorkerArchivesFailedFile(t *testing.T) {
	root := t.TempDir()
	store := &fakeDocumentStore{}
	worker := NewWorker(store, nil, root, 1)

	staged := stageFile(t, root, "1789807643538534200", "# 标题")
	// metadata 里已经有失败现场：归档这一步是来改 upload_path 的，不该把它抹掉。
	// 必须走 Marshal —— 手拼字符串的话，Windows 路径里的反斜杠会把 JSON 打断。
	payload, err := json.Marshal(map[string]any{
		"upload_path": staged,
		"stage":       "parse",
		"error":       "解析器不可用",
	})
	if err != nil {
		t.Fatalf("构造 metadata 失败: %v", err)
	}
	store.metadata = payload

	worker.archiveStagedFile(context.Background(), testDocumentID, staged)

	archived := filepath.Join(archiveDir(root, testDocumentID), "upload.md")
	if _, err := os.Stat(archived); err != nil {
		t.Fatalf("失败原件没有归档到 failed/<文档ID>/ 下: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(staged)); !os.IsNotExist(err) {
		t.Errorf("归档之后暂存目录应当消失，实际 stat 返回 %v", err)
	}

	var metadata map[string]any
	if err := json.Unmarshal(store.metadata, &metadata); err != nil {
		t.Fatalf("解析 metadata 失败: %v（原文 %s）", err, store.metadata)
	}
	if metadata["upload_path"] != archived {
		t.Errorf("upload_path 应当指向归档后的位置：期望 %q，实际 %v", archived, metadata["upload_path"])
	}
	if metadata["error"] != "解析器不可用" {
		t.Errorf("归档不该抹掉失败现场，实际 %v", metadata)
	}
}

// 原件已经不在时（metadata 里的路径早被人工动过、或文件被人删了）安静返回：
// 没有东西可归档，也就没有理由去改库。
func TestWorkerArchivesMissingFileQuietly(t *testing.T) {
	root := t.TempDir()
	store := &fakeDocumentStore{}
	worker := NewWorker(store, nil, root, 1)

	missing := filepath.Join(root, "pending", "1", "upload.md")
	worker.archiveStagedFile(context.Background(), testDocumentID, missing)

	if store.metadata != nil {
		t.Errorf("原件不在时不该动 metadata，实际 %v", store.metadata)
	}
	if _, err := os.Stat(archiveDir(root, testDocumentID)); !os.IsNotExist(err) {
		t.Errorf("原件不在时不该建出归档目录，实际 stat 返回 %v", err)
	}
}

// 重试之后再失败：这一回 worker 从 metadata 读到的 path 已经在归档目录里，
// source 与 target 是同一个 —— 归档必须原地不动。
//
// 踩过的坑：目标"先清后挪"是为了覆盖上一次那份，可在这里 target 就是 source，
// RemoveAll 会把唯一一份原件连目录一起删掉，之后重试永远报"原件已不在"。
func TestWorkerArchivesFileAlreadyInPlace(t *testing.T) {
	root := t.TempDir()
	store := &fakeDocumentStore{}
	worker := NewWorker(store, nil, root, 1)
	ctx := context.Background()

	staged := stageFile(t, root, "pending-1", "# 标题")
	worker.archiveStagedFile(ctx, testDocumentID, staged)

	archived := filepath.Join(archiveDir(root, testDocumentID), "upload.md")
	content, err := os.ReadFile(archived)
	if err != nil {
		t.Fatalf("第一次归档失败: %v", err)
	}

	// 重试：worker 拿到的 path 就是归档位置。
	worker.archiveStagedFile(ctx, testDocumentID, archived)

	again, err := os.ReadFile(archived)
	if err != nil {
		t.Fatalf("原件已在归档目录里时不该被删掉: %v", err)
	}
	if string(again) != string(content) {
		t.Errorf("原地归档不该改动内容：期望 %q，实际 %q", content, again)
	}
}

// 同一个文档重试之后又失败：归档目录里还躺着上一次那一份。
// 必须先清掉再挪 —— os.Rename 在目标已存在时会失败（Windows 上尤其），
// 那会让第二次归档整个丢掉，磁盘上只留下上一轮的旧文件。
func TestWorkerArchivesReplacesPreviousArchive(t *testing.T) {
	root := t.TempDir()
	store := &fakeDocumentStore{}
	worker := NewWorker(store, nil, root, 1)
	ctx := context.Background()

	archive := func(content string) {
		staged := stageFile(t, root, fmt.Sprintf("pending-%d", time.Now().UnixNano()), content)
		worker.archiveStagedFile(ctx, testDocumentID, staged)
	}

	archive("第一次上传的内容")
	archive("重试后仍然失败的内容")

	directory := archiveDir(root, testDocumentID)
	content, err := os.ReadFile(filepath.Join(directory, "upload.md"))
	if err != nil {
		t.Fatalf("读取归档文件失败: %v", err)
	}
	if string(content) != "重试后仍然失败的内容" {
		t.Errorf("归档目录里应当是最新那一份，实际 %q", content)
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("读取归档目录失败: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("归档目录里应当只有一份文件，实际 %d 份", len(entries))
	}
}
