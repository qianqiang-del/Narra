package objectstorage

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

// listPageSize 是 DeletePrefix 每轮列举/删除的对象数。OSS 单次 ListObjects 上限 1000，
// 删除接口上限也是 1000；一页一删，内存占用与进度都直观。
const listPageSize = 1000

// defaultTimeout 是单次对象操作的读写超时。
const defaultTimeout = 30 * time.Second

// OSSConfig 是阿里云 OSS 的接入配置。
type OSSConfig struct {
	// Endpoint 是 Bucket 所在地域的服务地址，如 oss-cn-hangzhou.aliyuncs.com；
	// 带不带 https:// 都可以，这里会归一化。
	Endpoint string
	Bucket   string
	// AccessKeyID / AccessKeySecret 是 RAM 用户的密钥；建议由环境变量注入，别提交进仓库。
	AccessKeyID     string
	AccessKeySecret string
	// Timeout 是单次请求的读写超时；<=0 时用 30s。连接超时固定 10s。
	Timeout time.Duration
}

// bucketAPI 把 *oss.Bucket 里用到的方法收窄成一个接口，方便测试给替身。
// *oss.Bucket 的方法签名与之完全一致，直接满足。
type bucketAPI interface {
	PutObjectFromFile(objectKey, filePath string, options ...oss.Option) error
	PutObject(objectKey string, reader io.Reader, options ...oss.Option) error
	GetObjectToFile(objectKey, filePath string, options ...oss.Option) error
	IsObjectExist(objectKey string, options ...oss.Option) (bool, error)
	DeleteObject(objectKey string, options ...oss.Option) error
	DeleteObjects(objectKeys []string, options ...oss.Option) (oss.DeleteObjectsResult, error)
	ListObjects(options ...oss.Option) (oss.ListObjectsResult, error)
}

// OSSClient 是 Client 的阿里云 OSS 实现。
type OSSClient struct {
	bucket  bucketAPI
	urlBase string
}

var _ Client = (*OSSClient)(nil)

// NewOSSClient 构造客户端。配置缺失时直接报错：存储是收录链路的硬依赖，
// 宁可启动失败，也不要等第一次上传才暴露。
func NewOSSClient(cfg OSSConfig) (*OSSClient, error) {
	endpoint := normalizeEndpoint(cfg.Endpoint)
	if endpoint == "" {
		return nil, errors.New("OSS endpoint 不能为空")
	}
	bucketName := strings.TrimSpace(cfg.Bucket)
	if bucketName == "" {
		return nil, errors.New("OSS bucket 不能为空")
	}
	accessKeyID := strings.TrimSpace(cfg.AccessKeyID)
	accessKeySecret := strings.TrimSpace(cfg.AccessKeySecret)
	if accessKeyID == "" || accessKeySecret == "" {
		return nil, errors.New("OSS AccessKey 不能为空")
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	readWriteSeconds := int64(timeout / time.Second)
	if readWriteSeconds < 1 {
		readWriteSeconds = 1
	}

	client, err := oss.New(endpoint, accessKeyID, accessKeySecret,
		oss.Timeout(10, readWriteSeconds))
	if err != nil {
		return nil, fmt.Errorf("创建 OSS 客户端失败: %w", err)
	}
	bucket, err := client.Bucket(bucketName)
	if err != nil {
		return nil, fmt.Errorf("获取 OSS Bucket %s 失败: %w", bucketName, err)
	}

	// 对外链接固定用 Bucket 默认域名：https://<bucket>.<endpoint>。
	// 公共读的 Bucket 直链不签名，浏览器可直接访问。
	urlBase := "https://" + bucketName + "." + endpoint
	return &OSSClient{bucket: bucket, urlBase: urlBase}, nil
}

// PutFile 实现 Client。
func (c *OSSClient) PutFile(key, filePath string) error {
	normalized := normalizeKey(key)
	if normalized == "" {
		return errors.New("对象 key 不能为空")
	}
	if err := c.bucket.PutObjectFromFile(normalized, filePath); err != nil {
		return fmt.Errorf("上传对象 %s 失败: %w", normalized, err)
	}
	return nil
}

// PutBytes 实现 Client。
func (c *OSSClient) PutBytes(key string, data []byte) error {
	normalized := normalizeKey(key)
	if normalized == "" {
		return errors.New("对象 key 不能为空")
	}
	if err := c.bucket.PutObject(normalized, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("上传对象 %s 失败: %w", normalized, err)
	}
	return nil
}

// GetToFile 实现 Client。
func (c *OSSClient) GetToFile(key, filePath string) error {
	normalized := normalizeKey(key)
	if normalized == "" {
		return errors.New("对象 key 不能为空")
	}
	if err := c.bucket.GetObjectToFile(normalized, filePath); err != nil {
		return fmt.Errorf("下载对象 %s 失败: %w", normalized, err)
	}
	return nil
}

// Exists 实现 Client。
func (c *OSSClient) Exists(key string) (bool, error) {
	normalized := normalizeKey(key)
	if normalized == "" {
		return false, errors.New("对象 key 不能为空")
	}
	exists, err := c.bucket.IsObjectExist(normalized)
	if err != nil {
		return false, fmt.Errorf("判断对象 %s 是否存在失败: %w", normalized, err)
	}
	return exists, nil
}

// Delete 实现 Client。
func (c *OSSClient) Delete(key string) error {
	normalized := normalizeKey(key)
	if normalized == "" {
		return errors.New("对象 key 不能为空")
	}
	if err := c.bucket.DeleteObject(normalized); err != nil {
		return fmt.Errorf("删除对象 %s 失败: %w", normalized, err)
	}
	return nil
}

// DeletePrefix 实现 Client：按前缀分页列举并整批删除。
//
// 分页而不是一次列全：一个文档的图片、一门课的音频数量都不大，但接口不该赌上限；
// 每页删完再取下一页，中途失败时已删的保持删除（清理动作天然可重入）。
func (c *OSSClient) DeletePrefix(keyPrefix string) error {
	prefix := normalizeKey(keyPrefix)
	if prefix == "" {
		// 空前缀等于清空整个 Bucket，这个能力绝不能从业务侧误触。
		return errors.New("拒绝删除空前缀")
	}
	listPrefix := prefix + "/"

	marker := ""
	for {
		result, err := c.bucket.ListObjects(
			oss.Prefix(listPrefix),
			oss.Marker(marker),
			oss.MaxKeys(listPageSize),
		)
		if err != nil {
			return fmt.Errorf("列举对象 %s 失败: %w", listPrefix, err)
		}
		if len(result.Objects) == 0 {
			return nil
		}

		keys := make([]string, 0, len(result.Objects))
		for _, object := range result.Objects {
			keys = append(keys, object.Key)
		}
		if _, err := c.bucket.DeleteObjects(keys, oss.DeleteObjectsQuiet(true)); err != nil {
			return fmt.Errorf("删除对象 %s 失败: %w", listPrefix, err)
		}
		if !result.IsTruncated {
			return nil
		}
		// 有些兼容实现不回 NextMarker，用本页最后一个 key 兜底继续。
		marker = result.NextMarker
		if marker == "" {
			marker = keys[len(keys)-1]
		}
	}
}

// URL 实现 Client。key 按段转义后再拼接：中文文件名、空格等都能得到合法链接。
func (c *OSSClient) URL(key string) string {
	normalized := normalizeKey(key)
	if normalized == "" {
		return c.urlBase
	}
	segments := strings.Split(normalized, "/")
	for index := range segments {
		segments[index] = url.PathEscape(segments[index])
	}
	return c.urlBase + "/" + strings.Join(segments, "/")
}

// normalizeEndpoint 统一 endpoint 的写法：去掉 scheme 与尾部斜杠。
//
// 保留端口（自定义域名可能带 :9000 这类端口），只处理前缀与末尾。
func normalizeEndpoint(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	endpoint = strings.TrimPrefix(endpoint, "https://")
	endpoint = strings.TrimPrefix(endpoint, "http://")
	return strings.TrimRight(endpoint, "/")
}

// normalizeKey 去掉 key 两端多余的斜杠；全部是斜杠时得到空串。
func normalizeKey(key string) string {
	return strings.Trim(strings.TrimSpace(key), "/")
}
