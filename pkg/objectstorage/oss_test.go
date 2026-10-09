package objectstorage

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

// fakeBucket 是 bucketAPI 的内存替身。
//
// ListObjects 不解析 options（oss.Option 改的是 SDK 私有结构，外部拿不到），
// 因此它要么一次返回全部对象，要么按调用次数依次吐预设页 —— 后者用来测分页循环。
type fakeBucket struct {
	objects map[string][]byte

	pages      []oss.ListObjectsResult // 非空时按调用次数依次返回，用完后返回空页
	listCalls  int
	deleted    [][]string
	putFileErr error
}

func newFakeBucket() *fakeBucket {
	return &fakeBucket{objects: map[string][]byte{}}
}

func (f *fakeBucket) PutObjectFromFile(objectKey, filePath string, options ...oss.Option) error {
	if f.putFileErr != nil {
		return f.putFileErr
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	f.objects[objectKey] = data
	return nil
}

func (f *fakeBucket) PutObject(objectKey string, reader io.Reader, options ...oss.Option) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	f.objects[objectKey] = data
	return nil
}

func (f *fakeBucket) GetObjectToFile(objectKey, filePath string, options ...oss.Option) error {
	data, ok := f.objects[objectKey]
	if !ok {
		return oss.ServiceError{Code: "NoSuchKey"}
	}
	return os.WriteFile(filePath, data, 0o644)
}

func (f *fakeBucket) IsObjectExist(objectKey string, options ...oss.Option) (bool, error) {
	_, ok := f.objects[objectKey]
	return ok, nil
}

func (f *fakeBucket) DeleteObject(objectKey string, options ...oss.Option) error {
	delete(f.objects, objectKey)
	return nil
}

func (f *fakeBucket) DeleteObjects(objectKeys []string, options ...oss.Option) (oss.DeleteObjectsResult, error) {
	f.deleted = append(f.deleted, append([]string(nil), objectKeys...))
	for _, key := range objectKeys {
		delete(f.objects, key)
	}
	return oss.DeleteObjectsResult{}, nil
}

func (f *fakeBucket) ListObjects(options ...oss.Option) (oss.ListObjectsResult, error) {
	if len(f.pages) > 0 {
		index := f.listCalls
		f.listCalls++
		if index >= len(f.pages) {
			return oss.ListObjectsResult{}, nil
		}
		return f.pages[index], nil
	}
	result := oss.ListObjectsResult{}
	for key := range f.objects {
		result.Objects = append(result.Objects, oss.ObjectProperties{Key: key})
	}
	return result, nil
}

func newTestClient(bucket bucketAPI, urlBase string) *OSSClient {
	return &OSSClient{bucket: bucket, urlBase: urlBase}
}

func TestURLUsesBucketDomainAndEscapesSegments(t *testing.T) {
	client := newTestClient(newFakeBucket(), "https://demo.oss-cn-hangzhou.aliyuncs.com")

	got := client.URL("/knowledge/images/7/abc 中文.png")
	want := "https://demo.oss-cn-hangzhou.aliyuncs.com/knowledge/images/7/abc%20%E4%B8%AD%E6%96%87.png"
	if got != want {
		t.Fatalf("URL() = %q, want %q", got, want)
	}
}

func TestPutGetExistsDelete(t *testing.T) {
	bucket := newFakeBucket()
	client := newTestClient(bucket, "https://demo.example.com")
	dir := t.TempDir()

	source := filepath.Join(dir, "upload.docx")
	if err := os.WriteFile(source, []byte("hello oss"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := client.PutFile("knowledge/uploads/1/upload.docx", source); err != nil {
		t.Fatalf("PutFile: %v", err)
	}

	exists, err := client.Exists("knowledge/uploads/1/upload.docx")
	if err != nil || !exists {
		t.Fatalf("Exists = %v, %v; want true, nil", exists, err)
	}

	dest := filepath.Join(dir, "back.docx")
	if err := client.GetToFile("knowledge/uploads/1/upload.docx", dest); err != nil {
		t.Fatalf("GetToFile: %v", err)
	}
	roundTrip, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(roundTrip) != "hello oss" {
		t.Fatalf("round trip = %q", roundTrip)
	}

	if err := client.Delete("knowledge/uploads/1/upload.docx"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	exists, err = client.Exists("knowledge/uploads/1/upload.docx")
	if err != nil || exists {
		t.Fatalf("after delete Exists = %v, %v; want false, nil", exists, err)
	}
}

func TestPutBytes(t *testing.T) {
	bucket := newFakeBucket()
	client := newTestClient(bucket, "https://demo.example.com")

	if err := client.PutBytes("knowledge/audio/9/1.wav", []byte("wav-bytes")); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}
	if got := string(bucket.objects["knowledge/audio/9/1.wav"]); got != "wav-bytes" {
		t.Fatalf("object = %q", got)
	}
}

func TestDeletePrefixOnlyTouchesPrefix(t *testing.T) {
	bucket := newFakeBucket()
	bucket.objects["knowledge/images/7/a.png"] = []byte("a")
	bucket.objects["knowledge/images/7/b.png"] = []byte("b")
	bucket.objects["knowledge/images/8/c.png"] = []byte("c")
	bucket.objects["knowledge/uploads/7/upload.pdf"] = []byte("d")
	// 替身解析不了 SDK 的私有 option，这里用预设页模拟"按前缀过滤后的真实返回"。
	bucket.pages = []oss.ListObjectsResult{
		{
			Objects: []oss.ObjectProperties{
				{Key: "knowledge/images/7/a.png"},
				{Key: "knowledge/images/7/b.png"},
			},
		},
	}
	client := newTestClient(bucket, "https://demo.example.com")

	if err := client.DeletePrefix("knowledge/images/7"); err != nil {
		t.Fatalf("DeletePrefix: %v", err)
	}
	if _, ok := bucket.objects["knowledge/images/7/a.png"]; ok {
		t.Error("前缀内的对象没有被删除")
	}
	if _, ok := bucket.objects["knowledge/images/7/b.png"]; ok {
		t.Error("前缀内的对象没有被删除")
	}
	for _, keep := range []string{"knowledge/images/8/c.png", "knowledge/uploads/7/upload.pdf"} {
		if _, ok := bucket.objects[keep]; !ok {
			t.Errorf("前缀外的对象 %s 被误删", keep)
		}
	}
}

func TestDeletePrefixPaginates(t *testing.T) {
	bucket := newFakeBucket()
	bucket.pages = []oss.ListObjectsResult{
		{
			IsTruncated: true,
			NextMarker:  "knowledge/audio/3/2.wav",
			Objects: []oss.ObjectProperties{
				{Key: "knowledge/audio/3/1.wav"},
				{Key: "knowledge/audio/3/2.wav"},
			},
		},
		{
			Objects: []oss.ObjectProperties{{Key: "knowledge/audio/3/3.wav"}},
		},
	}
	client := newTestClient(bucket, "https://demo.example.com")

	if err := client.DeletePrefix("knowledge/audio/3"); err != nil {
		t.Fatalf("DeletePrefix: %v", err)
	}
	if len(bucket.deleted) != 2 {
		t.Fatalf("删除批次数 = %d, want 2", len(bucket.deleted))
	}
	if got := strings.Join(bucket.deleted[1], ","); got != "knowledge/audio/3/3.wav" {
		t.Fatalf("第二批 = %q", got)
	}
}

func TestDeletePrefixRejectsEmptyPrefix(t *testing.T) {
	client := newTestClient(newFakeBucket(), "https://demo.example.com")
	if err := client.DeletePrefix("/"); err == nil {
		t.Fatal("空前缀应当被拒绝")
	}
}

func TestPutFileWrapsError(t *testing.T) {
	bucket := newFakeBucket()
	bucket.putFileErr = errors.New("network down")
	client := newTestClient(bucket, "https://demo.example.com")

	err := client.PutFile("knowledge/uploads/1/upload.pdf", "whatever.pdf")
	if err == nil || !strings.Contains(err.Error(), "network down") {
		t.Fatalf("PutFile error = %v; want wrapped network error", err)
	}
}

func TestNewOSSClientValidatesConfig(t *testing.T) {
	cases := []OSSConfig{
		{Bucket: "b", AccessKeyID: "id", AccessKeySecret: "sk"},                              // 缺 endpoint
		{Endpoint: "oss-cn-hangzhou.aliyuncs.com", AccessKeyID: "id", AccessKeySecret: "sk"}, // 缺 bucket
		{Endpoint: "oss-cn-hangzhou.aliyuncs.com", Bucket: "b"},                              // 缺密钥
	}
	for _, cfg := range cases {
		if _, err := NewOSSClient(cfg); err == nil {
			t.Fatalf("配置 %+v 应当被拒绝", cfg)
		}
	}
}
