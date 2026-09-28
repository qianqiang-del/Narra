package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
	"narra/internal/service"
	"narra/pkg/config"
	apperrors "narra/pkg/errors"
)

// 这个文件只测上传接口的传输契约：批量逐项结果、读取侧上限、旧单文件形状。
// 收录本身的队列判定与事务在 internal/rag 的用例里测，这里用替身把服务层替换掉。

// stubUploadService 记录收到的提交输入，按调用次序返回预设错误或成功的 pending 文档。
type stubUploadService struct {
	service.KnowledgeService

	submitted []requestdto.KnowledgeIngestFile
	// errs 按调用次序返回；不足或对应位置为 nil 时返回成功。
	errs []error
}

var _ service.KnowledgeService = (*stubUploadService)(nil)

func (s *stubUploadService) SubmitFile(_ context.Context, input requestdto.KnowledgeIngestFile) (responsedto.KnowledgeDocument, error) {
	index := len(s.submitted)
	s.submitted = append(s.submitted, input)
	if index < len(s.errs) && s.errs[index] != nil {
		return responsedto.KnowledgeDocument{}, s.errs[index]
	}
	return responsedto.KnowledgeDocument{
		ID:     uint64(index + 1),
		Title:  filepath.Base(input.Path),
		Status: entity.KnowledgeDocumentStatusPending,
	}, nil
}

// uploadTestFile 是一份待上传的测试文件。
type uploadTestFile struct {
	name    string
	content string
}

// newUploadController 造一个只服务上传用例的控制器，返回它与上传根目录。
func newUploadController(t *testing.T, svc service.KnowledgeService, limits config.KnowledgeIngestConfig) (*Controller, string) {
	t.Helper()
	root := t.TempDir()
	return NewController(svc, root, nil, limits), root
}

// uploadRequest 构造一次 multipart 上传请求。
//
// field 是文件字段名（files 或 file）；files 会按顺序写进同一个请求，
// 与浏览器多选后一次提交的形状一致。
func uploadRequest(t *testing.T, field string, files []uploadTestFile) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, file := range files {
		part, err := writer.CreateFormFile(field, file.name)
		if err != nil {
			t.Fatalf("构造 multipart 分段失败: %v", err)
		}
		if _, err := part.Write([]byte(file.content)); err != nil {
			t.Fatalf("写入 multipart 内容失败: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 multipart writer 失败: %v", err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/knowledge/documents", &body)
	ctx.Request.Header.Set("Content-Type", writer.FormDataContentType())
	return ctx, recorder
}

// envelope 是统一响应信封，data 先按原始 JSON 留着，由各用例按形状解析。
type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func decodeEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) envelope {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应不是 JSON: %v（原文 %s）", err, recorder.Body.String())
	}
	return env
}

// pendingEntries 返回 <uploadDir>/pending 下的目录条目。
func pendingEntries(t *testing.T, root string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "pending"))
	if err != nil {
		t.Fatalf("读取暂存根目录失败: %v", err)
	}
	return entries
}

// 批量上传的主路径：两个文件一次提交，202 + 逐项 pending，各自落在独立的随机目录里。
func TestUploadBatchAcceptsFiles(t *testing.T) {
	svc := &stubUploadService{}
	controller, root := newUploadController(t, svc, config.KnowledgeIngestConfig{})
	ctx, recorder := uploadRequest(t, uploadFieldPlural, []uploadTestFile{
		{name: "设计.md", content: "# 设计\n\n正文。"},
		{name: "架构.md", content: "# 架构\n\n正文。"},
	})

	controller.Upload(ctx)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("HTTP 状态 = %d，期望 202", recorder.Code)
	}
	env := decodeEnvelope(t, recorder)
	if env.Code != apperrors.CodeSuccess {
		t.Fatalf("信封 code = %d，期望成功", env.Code)
	}

	var batch responsedto.KnowledgeIngestBatch
	if err := json.Unmarshal(env.Data, &batch); err != nil {
		t.Fatalf("data 不是批量结果: %v", err)
	}
	if batch.Accepted != 2 || batch.Rejected != 0 {
		t.Fatalf("汇总 = %d 入队 / %d 被拒，期望 2 / 0", batch.Accepted, batch.Rejected)
	}
	if len(batch.Items) != 2 {
		t.Fatalf("逐项结果数 = %d，期望 2", len(batch.Items))
	}
	for index, item := range batch.Items {
		if item.Status != responsedto.KnowledgeIngestItemStatusPending {
			t.Errorf("第 %d 项状态 = %q，期望 pending", index+1, item.Status)
		}
		if item.DocumentID == nil {
			t.Errorf("第 %d 项缺少 document_id", index+1)
		}
	}
	if batch.Items[0].OriginalName != "设计.md" {
		t.Errorf("原始文件名 = %q，期望保留用户看到的名字", batch.Items[0].OriginalName)
	}

	// 落盘路径由服务端随机生成，与原始文件名无关：目录名必须是 32 位随机十六进制。
	if len(svc.submitted) != 2 {
		t.Fatalf("提交次数 = %d，期望 2", len(svc.submitted))
	}
	randomDir := regexp.MustCompile(`^[0-9a-f]{32}$`)
	for index, input := range svc.submitted {
		dir := filepath.Dir(input.Path)
		if !strings.HasPrefix(dir, filepath.Join(root, "pending")) {
			t.Errorf("第 %d 个文件落在受控目录之外: %s", index+1, dir)
		}
		if !randomDir.MatchString(filepath.Base(dir)) {
			t.Errorf("第 %d 个文件的目录名 %q 不是随机十六进制", index+1, filepath.Base(dir))
		}
		if _, err := os.Stat(input.Path); err != nil {
			t.Errorf("第 %d 个文件应当已落盘: %v", index+1, err)
		}
	}
}

// 单文件超限只拒它自己：同批其它文件照常入队，且超限文件根本不落盘、不提交。
func TestUploadRejectsOversizedFilePerItem(t *testing.T) {
	svc := &stubUploadService{}
	controller, root := newUploadController(t, svc, config.KnowledgeIngestConfig{
		MaxFiles:      10,
		MaxFileBytes:  10,
		MaxBatchBytes: 1024,
	})
	ctx, recorder := uploadRequest(t, uploadFieldPlural, []uploadTestFile{
		{name: "大文件.md", content: strings.Repeat("a", 50)},
		{name: "小文件.md", content: "abc"},
	})

	controller.Upload(ctx)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("HTTP 状态 = %d，期望 202（批量整体已受理）", recorder.Code)
	}
	var batch responsedto.KnowledgeIngestBatch
	if err := json.Unmarshal(decodeEnvelope(t, recorder).Data, &batch); err != nil {
		t.Fatalf("data 不是批量结果: %v", err)
	}
	if batch.Accepted != 1 || batch.Rejected != 1 {
		t.Fatalf("汇总 = %d / %d，期望 1 / 1", batch.Accepted, batch.Rejected)
	}
	if batch.Items[0].Status != responsedto.KnowledgeIngestItemStatusRejected ||
		!strings.Contains(batch.Items[0].Error, "超过上限") {
		t.Errorf("超限项 = %+v，期望 rejected 且说明超过上限", batch.Items[0])
	}
	if batch.Items[1].Status != responsedto.KnowledgeIngestItemStatusPending {
		t.Errorf("小文件应当照常入队，实际 %+v", batch.Items[1])
	}
	if len(svc.submitted) != 1 {
		t.Fatalf("提交次数 = %d，期望只提交小文件", len(svc.submitted))
	}
	if entries := pendingEntries(t, root); len(entries) != 1 {
		t.Errorf("暂存目录数 = %d，期望只有入队文件那一份", len(entries))
	}
}

// 提交失败（含队列已满）时要清掉已落盘的暂存目录：worker 从没见过它，
// 留下就是一份没有数据库行认领的孤儿。
func TestUploadDiscardsStagedFileWhenSubmitFails(t *testing.T) {
	svc := &stubUploadService{errs: []error{service.ErrIngestQueueFull}}
	controller, root := newUploadController(t, svc, config.KnowledgeIngestConfig{})
	ctx, recorder := uploadRequest(t, uploadFieldPlural, []uploadTestFile{
		{name: "设计.md", content: "# 设计"},
	})

	controller.Upload(ctx)

	var batch responsedto.KnowledgeIngestBatch
	if err := json.Unmarshal(decodeEnvelope(t, recorder).Data, &batch); err != nil {
		t.Fatalf("data 不是批量结果: %v", err)
	}
	if batch.Rejected != 1 || !strings.Contains(batch.Items[0].Error, "队列已满") {
		t.Fatalf("期望逐项 rejected 且说明队列已满，实际 %+v", batch)
	}
	if entries := pendingEntries(t, root); len(entries) != 0 {
		t.Errorf("提交失败后暂存目录应当被清掉，实际残留 %d 个", len(entries))
	}
}

// 文件数超限是请求级失败：这时已经安全枚举出了列表，但"取前 N 个、丢其余"
// 等于替用户做了丢弃决定，所以整批拒绝。
func TestUploadRejectsTooManyFiles(t *testing.T) {
	svc := &stubUploadService{}
	controller, _ := newUploadController(t, svc, config.KnowledgeIngestConfig{
		MaxFiles:      1,
		MaxFileBytes:  1024,
		MaxBatchBytes: 4096,
	})
	ctx, recorder := uploadRequest(t, uploadFieldPlural, []uploadTestFile{
		{name: "一.md", content: "a"},
		{name: "二.md", content: "b"},
	})

	controller.Upload(ctx)

	env := decodeEnvelope(t, recorder)
	if env.Code != apperrors.CodeBadRequest || !strings.Contains(env.Message, "最多上传 1 个文件") {
		t.Fatalf("信封 = %+v，期望 400 且说明文件数上限", env)
	}
	if len(svc.submitted) != 0 {
		t.Errorf("整批被拒时不该提交任何文件，实际 %d 次", len(svc.submitted))
	}
}

// 请求体超过 Content-Length 预检线时在读到任何 body 之前拒绝，客户端拿到干净的
// JSON 错误而不是连接重置。
func TestUploadRejectsOversizedBodyByContentLength(t *testing.T) {
	svc := &stubUploadService{}
	controller, _ := newUploadController(t, svc, config.KnowledgeIngestConfig{
		MaxFiles:      10,
		MaxFileBytes:  4 << 20,
		MaxBatchBytes: 100,
	})
	ctx, recorder := uploadRequest(t, uploadFieldPlural, []uploadTestFile{
		{name: "大文件.md", content: strings.Repeat("a", 2<<20)},
	})

	controller.Upload(ctx)

	env := decodeEnvelope(t, recorder)
	if env.Code != apperrors.CodeBadRequest || !strings.Contains(env.Message, "超过单次批量上限") {
		t.Fatalf("信封 = %+v，期望 400 且说明批量上限", env)
	}
	if len(svc.submitted) != 0 {
		t.Errorf("整批被拒时不该提交任何文件，实际 %d 次", len(svc.submitted))
	}
}

// 没有 Content-Length 的请求（chunked）由 MaxBytesReader 兜底：解析阶段报
// *http.MaxBytesError，同样整批拒绝。
func TestUploadRejectsOversizedBodyWithoutContentLength(t *testing.T) {
	svc := &stubUploadService{}
	controller, _ := newUploadController(t, svc, config.KnowledgeIngestConfig{
		MaxFiles:      10,
		MaxFileBytes:  4 << 20,
		MaxBatchBytes: 100,
	})
	ctx, recorder := uploadRequest(t, uploadFieldPlural, []uploadTestFile{
		{name: "大文件.md", content: strings.Repeat("a", 2<<20)},
	})
	ctx.Request.ContentLength = -1 // 模拟 chunked：预检让位给 MaxBytesReader

	controller.Upload(ctx)

	env := decodeEnvelope(t, recorder)
	if env.Code != apperrors.CodeBadRequest || !strings.Contains(env.Message, "批量上限") {
		t.Fatalf("信封 = %+v，期望 400 且说明批量上限", env)
	}
	if len(svc.submitted) != 0 {
		t.Errorf("整批被拒时不该提交任何文件，实际 %d 次", len(svc.submitted))
	}
}

// 旧的单 file 字段保持老契约：成功回文档对象（HTTP 200），队列满回 409 业务码。
func TestUploadLegacySingleFileKeepsShape(t *testing.T) {
	svc := &stubUploadService{}
	controller, _ := newUploadController(t, svc, config.KnowledgeIngestConfig{})
	ctx, recorder := uploadRequest(t, uploadFieldSingular, []uploadTestFile{
		{name: "设计.md", content: "# 设计"},
	})

	controller.Upload(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP 状态 = %d，期望 200（旧契约）", recorder.Code)
	}
	var document responsedto.KnowledgeDocument
	if err := json.Unmarshal(decodeEnvelope(t, recorder).Data, &document); err != nil {
		t.Fatalf("data 不是文档对象: %v", err)
	}
	if document.ID == 0 || document.Status != entity.KnowledgeDocumentStatusPending {
		t.Errorf("文档 = %+v，期望 pending 且带 ID", document)
	}
}

func TestUploadLegacySingleFileConflictWhenQueueFull(t *testing.T) {
	svc := &stubUploadService{errs: []error{fmt.Errorf("包装: %w", service.ErrIngestQueueFull)}}
	controller, _ := newUploadController(t, svc, config.KnowledgeIngestConfig{})
	ctx, recorder := uploadRequest(t, uploadFieldSingular, []uploadTestFile{
		{name: "设计.md", content: "# 设计"},
	})

	controller.Upload(ctx)

	env := decodeEnvelope(t, recorder)
	if env.Code != apperrors.CodeConflict {
		t.Fatalf("信封 code = %d，期望 %d（队列满按 409 处理）", env.Code, apperrors.CodeConflict)
	}
}

// 限制值随状态接口下发：值与配置同源，前端不必自己再写一份。
func TestUploadLimitsEchoesConfig(t *testing.T) {
	controller, _ := newUploadController(t, &stubUploadService{}, config.KnowledgeIngestConfig{
		MaxFiles:      3,
		MaxFileBytes:  2048,
		MaxBatchBytes: 8192,
	})
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	controller.UploadLimits(ctx)

	var limits responsedto.KnowledgeUploadLimits
	if err := json.Unmarshal(decodeEnvelope(t, recorder).Data, &limits); err != nil {
		t.Fatalf("data 不是限制值: %v", err)
	}
	if limits.MaxFiles != 3 || limits.MaxFileBytes != 2048 || limits.MaxBatchBytes != 8192 {
		t.Errorf("限制值 = %+v，期望与配置一致", limits)
	}
}
