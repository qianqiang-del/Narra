package documentimage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeImage 在临时目录里造一张"图片"（内容就是字节，扩展名由调用方给）。
func writeImage(t *testing.T, dir, name string, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入测试图片失败: %v", err)
	}
	return path
}

// 发布后图片应当落到 <knowledgeDir>/images/<文档ID>/ 下，URL 指向静态路由，
// 内容与源文件一致。
func TestPublishCopiesImageAndBuildsURL(t *testing.T) {
	knowledgeDir := t.TempDir()
	store, err := NewStore(knowledgeDir)
	if err != nil {
		t.Fatalf("创建图片存储失败: %v", err)
	}

	source := writeImage(t, t.TempDir(), "img_1.png", "png-bytes")
	urls, err := store.Publish(7, []string{source})
	if err != nil {
		t.Fatalf("发布图片失败: %v", err)
	}

	url, ok := urls[source]
	if !ok {
		t.Fatalf("返回值里缺少源路径 %q 的映射: %v", source, urls)
	}
	if !strings.HasPrefix(url, URLPrefix+"/7/") {
		t.Errorf("URL = %q，期望以 %q 开头", url, URLPrefix+"/7/")
	}
	if !strings.HasSuffix(url, ".png") {
		t.Errorf("URL = %q，期望保留 .png 后缀", url)
	}

	name := strings.TrimPrefix(url, URLPrefix+"/7/")
	published := filepath.Join(knowledgeDir, "images", "7", name)
	content, err := os.ReadFile(published)
	if err != nil {
		t.Fatalf("发布后的图片不存在: %v", err)
	}
	if string(content) != "png-bytes" {
		t.Errorf("发布后的内容 = %q，期望与源文件一致", content)
	}
	if store.ImagesDir() != filepath.Join(knowledgeDir, "images") {
		t.Errorf("ImagesDir = %q，与静态路由的挂载点不一致", store.ImagesDir())
	}
}

// 同样的内容发布两次应当落到同一个文件上（内容哈希寻址）：重试重新解析时
// 临时文件名会变，只有内容哈希能保证幂等，不会越重试堆越多重复图。
func TestPublishIsIdempotentForSameContent(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("创建图片存储失败: %v", err)
	}
	sourceDir := t.TempDir()
	first := writeImage(t, sourceDir, "img_a.png", "same-bytes")
	second := writeImage(t, sourceDir, "img_b.jpg", "same-bytes")

	firstURLs, err := store.Publish(7, []string{first})
	if err != nil {
		t.Fatalf("第一次发布失败: %v", err)
	}
	secondURLs, err := store.Publish(7, []string{second})
	if err != nil {
		t.Fatalf("第二次发布失败: %v", err)
	}

	// 后缀不同（.png / .jpg）时目标文件名不同，这是有意的：URL 里的后缀参与
	// 静态服务的 Content-Type 判断，不能因为内容相同就把它抹掉。这里验的是
	// "同一路径、同一后缀"的重复发布命中同一个文件。
	again, err := store.Publish(7, []string{writeImage(t, sourceDir, "img_c.png", "same-bytes")})
	if err != nil {
		t.Fatalf("第三次发布失败: %v", err)
	}
	firstURL := firstURLs[first]
	if again[filepath.Join(sourceDir, "img_c.png")] != firstURL {
		t.Errorf("同内容同后缀的重复发布应当命中同一个 URL，实际 %v 与 %v", again, firstURLs)
	}
	if secondURLs[second] == firstURL {
		t.Errorf("不同后缀的目标文件名不该重合: %v", secondURLs[second])
	}

	names, err := os.ReadDir(filepath.Join(store.ImagesDir(), "7"))
	if err != nil {
		t.Fatalf("读取发布目录失败: %v", err)
	}
	if len(names) != 2 {
		t.Errorf("发布目录里应当只有 2 个文件（同内容同后缀去重），实际 %d 个", len(names))
	}
}

// 源文件不存在（临时目录被提前清掉等）时必须报错：调用方据此整篇失败，
// 而不是把本地路径留在正文里。
func TestPublishReportsMissingSource(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("创建图片存储失败: %v", err)
	}
	if _, err := store.Publish(7, []string{filepath.Join(t.TempDir(), "gone.png")}); err == nil {
		t.Fatal("源文件不存在时发布必须报错")
	}
}

// 删除文档只清它自己的图片目录，不能碰别的文档。
func TestRemoveDocumentOnlyRemovesTarget(t *testing.T) {
	knowledgeDir := t.TempDir()
	store, err := NewStore(knowledgeDir)
	if err != nil {
		t.Fatalf("创建图片存储失败: %v", err)
	}
	for _, id := range []uint64{7, 8} {
		if _, err := store.Publish(id, []string{writeImage(t, t.TempDir(), "img.png", "bytes")}); err != nil {
			t.Fatalf("发布文档 %d 的图片失败: %v", id, err)
		}
	}

	if err := RemoveDocument(knowledgeDir, 7); err != nil {
		t.Fatalf("清理文档图片失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(knowledgeDir, "images", "7")); !os.IsNotExist(err) {
		t.Errorf("文档 7 的图片目录应当已被删除，实际: %v", err)
	}
	if _, err := os.Stat(filepath.Join(knowledgeDir, "images", "8")); err != nil {
		t.Errorf("文档 8 的图片目录不该受影响: %v", err)
	}

	// 幂等：目录已经不在时再删一次不该报错。
	if err := RemoveDocument(knowledgeDir, 7); err != nil {
		t.Errorf("重复清理应当幂等，实际: %v", err)
	}
}

// 没配置目录时拒绝清理，避免按当前工作目录误删。
func TestRemoveDocumentRejectsEmptyDir(t *testing.T) {
	if err := RemoveDocument("", 7); err == nil {
		t.Fatal("目录未配置时应当报错")
	}
}

// 后缀不在白名单时丢掉后缀，文件名仍然合法。
func TestPublishDropsUnsafeExtension(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("创建图片存储失败: %v", err)
	}
	source := writeImage(t, t.TempDir(), "img.weird ext", "bytes")
	urls, err := store.Publish(7, []string{source})
	if err != nil {
		t.Fatalf("发布图片失败: %v", err)
	}
	if strings.Contains(urls[source], " ") {
		t.Errorf("URL 里不该出现原文件名的怪后缀: %q", urls[source])
	}
}
