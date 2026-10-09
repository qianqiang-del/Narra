// Package objectstorage 是对象存储的最小依赖面：把本地文件放进远端、按 key 取回、
// 删除并生成对外可访问的 URL。
//
// 第一版实现是阿里云 OSS（见 oss.go），调用方只依赖 Client 接口 —— 换厂商、
// 换测试替身都不动业务代码。知识库原件、文档图片与课堂音频三处共用这一个接口，
// key 的空间划分由各调用方决定（knowledge/uploads、knowledge/images、knowledge/audio）。
package objectstorage

// Client 是业务需要的最小操作面。key 是对象在 Bucket 内的完整路径（不含前导 /）。
//
// 刻意没有 ctx 参数：OSS SDK 的一次调用不接 ctx，超时在客户端构造时定死
// （见 OSSConfig.Timeout）。假装能取消只会给调用方错误的安全感。
type Client interface {
	// PutFile 把一个本地文件上传为 key；同 key 覆盖，天然幂等。
	PutFile(key, filePath string) error
	// PutBytes 把小块内存数据上传为 key（课堂音频走这条）。
	PutBytes(key string, data []byte) error
	// GetToFile 把 key 下载到本地 filePath；目标目录必须已存在。
	GetToFile(key, filePath string) error
	// Exists 判断 key 是否存在。
	Exists(key string) (bool, error)
	// Delete 删除一个 key；key 不存在不报错（OSS 语义如此，也符合清理方的期望）。
	Delete(key string) error
	// DeletePrefix 删除前缀下的全部对象，供文档图片、整门课音频这类"按目录清理"用。
	DeletePrefix(keyPrefix string) error
	// URL 返回 key 的对外访问地址；配置了自定义域名时用它，否则用 Bucket 默认域名。
	URL(key string) string
}
