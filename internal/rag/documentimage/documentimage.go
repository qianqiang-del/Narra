// Package documentimage 把解析产出的图片发布到本地持久目录，并生成可访问的 URL。
//
// 解析脚本把图片导出到临时目录（见 pkg/documentparser.Result.WorkDir），而临时目录
// 在收录过程中会被清理（Result.Cleanup），所以图片必须先在收录链路里"发布"到稳定
// 位置，再把 Markdown 里的本地路径替换成 URL（回填）。第一版部署在单机上，发布目标
// 就是本地磁盘，由 internal/app 通过静态路由对外提供；将来要换对象存储时，替换本包
// 实现即可，调用方只依赖 rag.ImagePublisher 这个窄接口。
package documentimage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// URLPrefix 是对外暴露知识图片的 URL 前缀，必须与 internal/app 注册的静态路由一致。
	// 用相对路径（不带 scheme / host）与音频一致：换域名、加反代都不用改库里数据。
	URLPrefix = "/knowledge/images"

	// imagesSubdir 是知识资产根目录下的图片子目录。
	// 加这一层是为了给将来的其他资产类型留位置，也让静态路由的挂载点一目了然。
	imagesSubdir = "images"

	// hashChars 是文件名里内容哈希取的前缀长度（十六进制字符）。
	// 单篇文档的图片数量有限，64 位前缀的碰撞概率可以忽略，文件名也不至于太长。
	hashChars = 16
)

// Store 是本地图片存储：图片落在 <knowledgeDir>/images/<文档ID>/ 下。
//
// 按文档分目录，文档删除时整目录清掉即可（见 RemoveDocument）；同一文档内按内容
// 哈希命名，重试重新解析时同一张图命中同一个名字，不会重复堆文件。
type Store struct {
	imagesDir string
}

// NewStore 创建本地图片存储并建好目录。knowledgeDir 为空或不可用时返回错误：
// 图片目录是收录链路的硬依赖，宁可启动失败也不要等第一次解析才暴露。
func NewStore(knowledgeDir string) (*Store, error) {
	if strings.TrimSpace(knowledgeDir) == "" {
		return nil, fmt.Errorf("知识图片目录不能为空")
	}
	root, err := filepath.Abs(knowledgeDir)
	if err != nil {
		return nil, fmt.Errorf("解析知识图片目录失败: %w", err)
	}
	imagesDir := filepath.Join(root, imagesSubdir)
	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建知识图片目录失败: %w", err)
	}
	return &Store{imagesDir: imagesDir}, nil
}

// ImagesDir 返回图片根目录的绝对路径，供静态路由挂载。
func (s *Store) ImagesDir() string { return s.imagesDir }

// Publish 把 paths 里的图片复制进持久目录，返回"原路径 → 对外 URL"的映射。
//
// 任何一张图片发布失败都返回错误：调用方（收录链路）据此整篇失败，而不是把
// 本地路径留在正文里、等临时目录一删全变死链。已经复制成功的那些不回收 ——
// 重试重新解析会命中同样的内容哈希，下一次调用直接覆盖，不产生重复文件。
func (s *Store) Publish(documentID uint64, paths []string) (map[string]string, error) {
	urls := make(map[string]string, len(paths))
	if len(paths) == 0 {
		return urls, nil
	}

	dir := s.documentDir(documentID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建文档图片目录失败: %w", err)
	}
	for _, source := range paths {
		name, err := publishFile(dir, source)
		if err != nil {
			return nil, err
		}
		urls[source] = URLPrefix + "/" + strconv.FormatUint(documentID, 10) + "/" + name
	}
	return urls, nil
}

// RemoveDocument 删掉一篇文档发布出去的全部图片，供文档删除时清理。
//
// 目录由文档 ID（uint64）拼出，不存在 upload_path 那种"metadata 里写什么就删什么"
// 的越界风险；目录不存在不算错误，删除动作因此是幂等的。
func RemoveDocument(knowledgeDir string, documentID uint64) error {
	if strings.TrimSpace(knowledgeDir) == "" {
		return fmt.Errorf("知识图片目录未配置")
	}
	root, err := filepath.Abs(knowledgeDir)
	if err != nil {
		return fmt.Errorf("解析知识图片目录失败: %w", err)
	}
	return os.RemoveAll(filepath.Join(root, imagesSubdir, strconv.FormatUint(documentID, 10)))
}

// documentDir 返回一篇文档的图片目录。
func (s *Store) documentDir(documentID uint64) string {
	return filepath.Join(s.imagesDir, strconv.FormatUint(documentID, 10))
}

// publishFile 复制一张图片到 dir，返回它落盘后的文件名。
//
// 先写同目录的临时文件、同时算内容哈希，最后改名到 <哈希><后缀>：哈希要在改名
// 之前算出来才能用它当文件名；临时文件与目标同目录保证改名是原子的（跨目录改名
// 可能退化成复制，中断时留下半张图）。
func publishFile(dir, source string) (string, error) {
	file, err := os.Open(source)
	if err != nil {
		return "", fmt.Errorf("读取解析图片失败: %w", err)
	}
	defer file.Close()

	temp, err := os.CreateTemp(dir, ".publish-*")
	if err != nil {
		return "", fmt.Errorf("创建图片临时文件失败: %w", err)
	}
	tempName := temp.Name()
	// 改名成功后 tempName 已不存在，这里重复一次 Remove 是幂等兜底：
	// 任何一条错误路径退出时都不该留下临时文件。
	defer func() { _ = os.Remove(tempName) }()

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temp, hasher), file); err != nil {
		_ = temp.Close()
		return "", fmt.Errorf("写入图片临时文件失败: %w", err)
	}
	if err := temp.Close(); err != nil {
		return "", fmt.Errorf("关闭图片临时文件失败: %w", err)
	}

	name := hex.EncodeToString(hasher.Sum(nil))[:hashChars] + imageExt(source)
	// 目标已存在时 os.Rename 直接覆盖：名字由内容哈希决定，覆盖的是同样的字节，
	// 重试重传因此是幂等的。
	if err := os.Rename(tempName, filepath.Join(dir, name)); err != nil {
		return "", fmt.Errorf("落盘解析图片失败: %w", err)
	}
	return name, nil
}

// imageExt 提取图片后缀，只保留小写字母数字组成的短后缀。
//
// 后缀来自解析脚本按 MIME 拼出的文件名，正常是 .png / .jpeg 这类；异常取值
// （含路径分隔符、超长等）直接丢掉后缀，静态服务按内容嗅探仍能给出正确的
// Content-Type，不影响图片显示。
func imageExt(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if len(ext) < 2 || len(ext) > 10 {
		return ""
	}
	for _, char := range ext[1:] {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') {
			return ""
		}
	}
	return ext
}
