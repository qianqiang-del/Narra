package documentimage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeObjectClient 是 objectstorage.Client 的内存替身，只实现图片发布用到的部分。
type fakeObjectClient struct {
	uploads         []string // 每次 PutFile 的 "key <- filePath"
	deletedPrefixes []string
}

func (f *fakeObjectClient) PutFile(key, filePath string) error {
	f.uploads = append(f.uploads, key+" <- "+filePath)
	return nil
}

func (f *fakeObjectClient) PutBytes(string, []byte) error  { return nil }
func (f *fakeObjectClient) GetToFile(string, string) error { return nil }
func (f *fakeObjectClient) Exists(string) (bool, error)    { return true, nil }
func (f *fakeObjectClient) Delete(string) error            { return nil }

func (f *fakeObjectClient) DeletePrefix(keyPrefix string) error {
	f.deletedPrefixes = append(f.deletedPrefixes, keyPrefix)
	return nil
}

func (f *fakeObjectClient) URL(key string) string {
	return "https://cdn.example.com/" + key
}

func TestOSSStorePublishUsesContentHashAndReturnsURL(t *testing.T) {
	client := &fakeObjectClient{}
	store, err := NewOSSStore(client)
	if err != nil {
		t.Fatalf("NewOSSStore: %v", err)
	}

	dir := t.TempDir()
	first := filepath.Join(dir, "a.png")
	second := filepath.Join(dir, "b.png")
	if err := os.WriteFile(first, []byte("same-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("same-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	urls, err := store.Publish(7, []string{first, second})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(urls) != 2 {
		t.Fatalf("urls 数量 = %d, want 2", len(urls))
	}
	if !strings.HasPrefix(urls[first], "https://cdn.example.com/knowledge/images/7/") {
		t.Fatalf("URL = %q", urls[first])
	}
	if !strings.HasSuffix(urls[first], ".png") {
		t.Fatalf("URL 应保留后缀: %q", urls[first])
	}
	if urls[first] != urls[second] {
		t.Fatalf("同内容图片应命中同一 key: %q vs %q", urls[first], urls[second])
	}
	if len(client.uploads) != 2 {
		t.Fatalf("上传次数 = %d, want 2（同 key 覆盖写）", len(client.uploads))
	}
}

func TestOSSStoreRemoveDocumentDeletesPrefix(t *testing.T) {
	client := &fakeObjectClient{}
	store, err := NewOSSStore(client)
	if err != nil {
		t.Fatalf("NewOSSStore: %v", err)
	}

	if err := store.RemoveDocument(42); err != nil {
		t.Fatalf("RemoveDocument: %v", err)
	}
	if len(client.deletedPrefixes) != 1 || client.deletedPrefixes[0] != "knowledge/images/42" {
		t.Fatalf("deletedPrefixes = %v", client.deletedPrefixes)
	}
}

func TestOSSStoreRejectsNilClient(t *testing.T) {
	if _, err := NewOSSStore(nil); err == nil {
		t.Fatal("nil 客户端应当被拒绝")
	}
}
