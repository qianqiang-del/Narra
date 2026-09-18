package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
	"narra/internal/rag"
)

const (
	// defaultPageSize / maxPageSize 列表分页的默认值与上限。
	defaultPageSize = 20
	maxPageSize     = 100
)

// documentQuerier 是本服务对文档查询的最小依赖面。
//
// 比 repository.KnowledgeDocumentRepository 窄：这份服务只负责列表与详情，
// 状态推进和切片替换是收录链路（rag.DocumentStore）的事，这里看不见它们。
type documentQuerier interface {
	// List 按创建时间倒序分页返回文档，同时给出总数。
	List(ctx context.Context, offset, limit int) ([]entity.KnowledgeDocument, int64, error)

	// GetByID 按主键取文档。查不到返回 gorm.ErrRecordNotFound。
	GetByID(ctx context.Context, id uint64) (*entity.KnowledgeDocument, error)

	// CountChunksByDocument 统计每篇文档的切片数，只返回入参里出现过的 ID。
	CountChunksByDocument(ctx context.Context, documentIDs []uint64) (map[uint64]int64, error)
}

// ingester 是本服务对收录能力的最小依赖面。
//
// 与 mcp_server_service 里的 mcpRuntime 同一路数：服务层负责 HTTP 面（DTO 进、DTO 出），
// 收录链路本身归 internal/rag。定义接口而不直接依赖 *rag.Ingester，
// 是为了让服务层的测试不必真的走一遍切分与向量化。
type ingester interface {
	IngestFile(ctx context.Context, input rag.FileInput) (rag.IngestResult, error)
	IngestText(ctx context.Context, input rag.TextInput) (rag.IngestResult, error)
}

type asyncIngester interface {
	SubmitFile(context.Context, rag.FileInput) (rag.IngestResult, error)
}

func (s *knowledgeService) SubmitFile(ctx context.Context, input requestdto.KnowledgeIngestFile) (responsedto.KnowledgeDocument, error) {
	async, ok := s.ingester.(asyncIngester)
	if !ok {
		return responsedto.KnowledgeDocument{}, fmt.Errorf("知识库异步收录不可用")
	}
	result, err := async.SubmitFile(ctx, rag.FileInput{Path: input.Path, Title: input.Title, SourceType: input.SourceType, SourceURI: input.SourceURI})
	return toDocumentResponse(result.Document, result.Chunks), err
}

// knowledgeService 是 KnowledgeService 的实现。
//
// 它只做三件事：把 HTTP DTO 翻成收录链路的输入、把 entity 翻成 HTTP DTO、
// 分页查询文档。收录本身的编排（状态机、切分、向量化、一个事务落三张表）全在 internal/rag ——
// 这里是"面"，那里是"里"。
type knowledgeService struct {
	documents documentQuerier
	ingester  ingester
	uploadDir string
}

var _ KnowledgeService = (*knowledgeService)(nil)

// NewKnowledgeService 创建知识库服务。
func NewKnowledgeService(documents documentQuerier, ingester ingester, uploadDirs ...string) KnowledgeService {
	uploadDir := ""
	if len(uploadDirs) > 0 {
		uploadDir = uploadDirs[0]
	}
	return &knowledgeService{documents: documents, ingester: ingester, uploadDir: uploadDir}
}

// IngestFile 收录一份文件，并把结果翻成对外的文档结构。
//
// 失败时返回的响应体里也带着那份文档：它已经写进库了（status = failed，
// 失败阶段与原因在 metadata）。接口层现在只用错误信息，但这个值必须能拿到 ——
// 收录异步化之后，它就是查询一次上传进度的入口。
func (s *knowledgeService) IngestFile(
	ctx context.Context,
	input requestdto.KnowledgeIngestFile,
) (responsedto.KnowledgeDocument, error) {
	result, err := s.ingester.IngestFile(ctx, rag.FileInput{
		Path:       input.Path,
		Title:      input.Title,
		SourceType: input.SourceType,
		SourceURI:  input.SourceURI,
	})
	return toDocumentResponse(result.Document, result.Chunks), err
}

// IngestText 直接收录一段正文，跳过解析这一步。
func (s *knowledgeService) IngestText(
	ctx context.Context,
	input requestdto.KnowledgeIngestText,
) (responsedto.KnowledgeDocument, error) {
	result, err := s.ingester.IngestText(ctx, rag.TextInput{
		Title:      input.Title,
		Content:    input.Content,
		SourceType: input.SourceType,
		SourceURI:  input.SourceURI,
	})
	return toDocumentResponse(result.Document, result.Chunks), err
}

// NormalizePage 归一化分页参数：page 从 1 起，size 落在 [1, MaxPageSize]。
//
// 导出它是为了让接口层用同一套口径组装响应体。接口层如果按用户传来的原值回填
// "page / size"，用户请求 size=500 时会收到一个"每页 500 条"、实际只有 100 条的响应，
// 前端据此算出来的页码全是错的。
func NormalizePage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}
	return page, size
}

// List 分页返回文档，并补齐每篇的切片数。
func (s *knowledgeService) List(ctx context.Context, page, size int) ([]responsedto.KnowledgeDocument, int64, error) {
	page, size = NormalizePage(page, size)

	documents, total, err := s.documents.List(ctx, (page-1)*size, size)
	if err != nil {
		return nil, 0, fmt.Errorf("查询知识文档失败: %w", err)
	}

	ids := make([]uint64, len(documents))
	for index, document := range documents {
		ids[index] = document.ID
	}
	counts, err := s.documents.CountChunksByDocument(ctx, ids)
	if err != nil {
		return nil, 0, fmt.Errorf("统计切片数量失败: %w", err)
	}

	out := make([]responsedto.KnowledgeDocument, len(documents))
	for index, document := range documents {
		current := document
		out[index] = toDocumentResponse(&current, int(counts[current.ID]))
	}
	return out, total, nil
}

// Get 返回单篇文档。
func (s *knowledgeService) Get(ctx context.Context, id uint64) (responsedto.KnowledgeDocument, error) {
	document, err := s.documents.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return responsedto.KnowledgeDocument{}, fmt.Errorf("知识文档 %d 不存在", id)
		}
		return responsedto.KnowledgeDocument{}, fmt.Errorf("查询知识文档失败: %w", err)
	}

	counts, err := s.documents.CountChunksByDocument(ctx, []uint64{id})
	if err != nil {
		return responsedto.KnowledgeDocument{}, fmt.Errorf("统计切片数量失败: %w", err)
	}
	return toDocumentResponse(document, int(counts[id])), nil
}

// toDocumentResponse 把实体转成对外结构。
//
// 正文只用来算字符数，不出网：一篇文档的正文可能有几十万字，
// 列表接口带上它会让响应大到没有必要。
//
// document 为 nil 时返回零值：收录链路在"连文档行都没建起来"的情况下会返回空结果
// （例如路径为空、来源类型非法），那种时候响应体没有内容可填，但调用方拿到的
// 错误信息是完整的。
func (s *knowledgeService) Preview(ctx context.Context, id uint64) (responsedto.KnowledgeDocumentPreview, error) {
	document, err := s.documents.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return responsedto.KnowledgeDocumentPreview{}, fmt.Errorf("知识文档 %d 不存在", id)
		}
		return responsedto.KnowledgeDocumentPreview{}, fmt.Errorf("查询知识文档失败: %w", err)
	}
	counts, err := s.documents.CountChunksByDocument(ctx, []uint64{id})
	if err != nil {
		return responsedto.KnowledgeDocumentPreview{}, fmt.Errorf("统计切片数量失败: %w", err)
	}
	return responsedto.KnowledgeDocumentPreview{KnowledgeDocument: toDocumentResponse(document, int(counts[id])), Content: document.Content}, nil
}

func (s *knowledgeService) Delete(ctx context.Context, id uint64) error {
	document, err := s.documents.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("知识文档 %d 不存在", id)
		}
		return fmt.Errorf("查询知识文档失败: %w", err)
	}
	deleter, ok := s.documents.(interface {
		Delete(context.Context, uint64) error
	})
	if !ok {
		return fmt.Errorf("知识库存储不支持删除")
	}
	if err := deleter.Delete(ctx, id); err != nil {
		return fmt.Errorf("删除知识文档失败: %w", err)
	}
	if path := metadataUploadPath(document.Metadata); path != "" && s.isUploadPath(path) {
		_ = os.RemoveAll(filepath.Dir(path))
	}
	return nil
}

func metadataUploadPath(raw json.RawMessage) string {
	var metadata map[string]any
	if json.Unmarshal(raw, &metadata) != nil {
		return ""
	}
	path, _ := metadata["upload_path"].(string)
	return strings.TrimSpace(path)
}

func (s *knowledgeService) isUploadPath(path string) bool {
	if strings.TrimSpace(s.uploadDir) == "" {
		return false
	}
	root, err := filepath.Abs(s.uploadDir)
	if err != nil {
		return false
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func toDocumentResponse(document *entity.KnowledgeDocument, chunks int) responsedto.KnowledgeDocument {
	if document == nil {
		return responsedto.KnowledgeDocument{}
	}

	out := responsedto.KnowledgeDocument{
		ID:         document.ID,
		Title:      document.Title,
		SourceType: document.SourceType,
		Enabled:    document.Enabled,
		Status:     document.Status,
		Parser:     parserName(document.Metadata),
		Error:      failureReason(document.Metadata),
		Chunks:     chunks,
		Characters: len([]rune(document.Content)),
		CreatedAt:  document.CreatedAt,
		UpdatedAt:  document.UpdatedAt,
	}
	if document.SourceURI != nil {
		out.SourceURI = *document.SourceURI
	}
	return out
}

func parserName(metadata json.RawMessage) string {
	if len(metadata) == 0 {
		return ""
	}
	var payload struct {
		Parser string `json:"parser"`
	}
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return ""
	}
	return payload.Parser
}

// failureReason 从 metadata 里取出失败原因。
//
// 只认 error 这一个键：metadata 的完整结构会随实现变化，前端不该依赖它，
// 它需要的只是一句能显示给用户的话。
func failureReason(metadata json.RawMessage) string {
	if len(metadata) == 0 {
		return ""
	}
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return ""
	}
	return payload.Error
}
