package documentimage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"

	"narra/pkg/objectstorage"
)

// objectKeyPrefix 是文档图片在对象存储里的 key 空间，与本地布局 <root>/images/<文档ID>/ 对齐。
const objectKeyPrefix = "knowledge/images"

// OSSStore 把解析产出的图片发布到对象存储，URL 由客户端按配置（Bucket 域名或自定义域名）生成。
//
// 与本地实现的关键约定一致：按文档 ID 分目录、按内容哈希命名 —— 重试重新解析同一张图
// 命中同一个 key，覆盖写天然幂等；删除文档时按前缀整目录清掉。
type OSSStore struct {
	client objectstorage.Client
}

// NewOSSStore 构造对象存储图片发布器。
func NewOSSStore(client objectstorage.Client) (*OSSStore, error) {
	if client == nil {
		return nil, fmt.Errorf("对象存储客户端不能为空")
	}
	return &OSSStore{client: client}, nil
}

// Publish 把 paths 里的图片逐张上传，返回"原路径 → 对外 URL"的映射。
//
// 任何一张失败都整体失败：调用方（收录链路）据此把整篇解析判失败，而不是让
// Markdown 里留下一半有效、一半缺失的链接。已上传的那些不回收 —— key 由内容哈希决定，
// 重试会覆盖同名对象，不会产生重复文件。
func (s *OSSStore) Publish(documentID uint64, paths []string) (map[string]string, error) {
	urls := make(map[string]string, len(paths))
	for _, source := range paths {
		name, err := contentName(source)
		if err != nil {
			return nil, err
		}
		key := objectKey(documentID, name)
		if err := s.client.PutFile(key, source); err != nil {
			return nil, fmt.Errorf("发布解析图片失败: %w", err)
		}
		urls[source] = s.client.URL(key)
	}
	return urls, nil
}

// RemoveDocument 按前缀删除一篇文档的全部图片；对象不存在时静默成功（幂等）。
func (s *OSSStore) RemoveDocument(documentID uint64) error {
	return s.client.DeletePrefix(objectKey(documentID, ""))
}

// objectKey 拼一篇文档的图片 key；name 为空时返回文档目录本身。
func objectKey(documentID uint64, name string) string {
	prefix := objectKeyPrefix + "/" + strconv.FormatUint(documentID, 10)
	if name == "" {
		return prefix
	}
	return prefix + "/" + name
}

// contentName 按内容哈希生成图片文件名：<sha256 前 16 位><合法后缀>。
//
// 与本地实现 publishFile 同一口径：同内容同名，重传与重试都是覆盖写。
func contentName(source string) (string, error) {
	file, err := os.Open(source)
	if err != nil {
		return "", fmt.Errorf("读取解析图片失败: %w", err)
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("计算图片哈希失败: %w", err)
	}
	return hex.EncodeToString(hasher.Sum(nil))[:hashChars] + imageExt(source), nil
}
