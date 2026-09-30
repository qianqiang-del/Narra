package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	responsedto "narra/internal/model/dto/response"
	"narra/internal/service"
	"narra/pkg/config"
	apperrors "narra/pkg/errors"
)

// 这个文件测"重新向量化"两个接口的传输契约：成功形状、状态码映射。
// 批量入队本身的判定在 internal/service 与 internal/rag 的用例里测，这里用替身。

// stubReembedService 是只服务向量体检与重新向量化的替身。
type stubReembedService struct {
	service.KnowledgeService

	status responsedto.KnowledgeEmbeddingStatus
	result responsedto.KnowledgeReembedResult
	err    error
}

var _ service.KnowledgeService = (*stubReembedService)(nil)

func (s *stubReembedService) EmbeddingStatus(context.Context) (responsedto.KnowledgeEmbeddingStatus, error) {
	return s.status, s.err
}

func (s *stubReembedService) Reembed(context.Context) (responsedto.KnowledgeReembedResult, error) {
	return s.result, s.err
}

// newReembedController 造一个只服务这两个接口的控制器。
func newReembedController(t *testing.T, svc service.KnowledgeService) *Controller {
	t.Helper()
	return NewController(svc, t.TempDir(), nil, config.KnowledgeIngestConfig{})
}

// reembedContext 造一个方法固定的测试上下文；两个接口都不收请求体。
func reembedContext(t *testing.T, method string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, "/api/v1/knowledge/documents/reembed", nil)
	return ctx, recorder
}

// 体检接口原样返回服务层结果：模型名、维度、缺向量的文档数。
func TestEmbeddingStatusReturnsPayload(t *testing.T) {
	svc := &stubReembedService{status: responsedto.KnowledgeEmbeddingStatus{
		Model:            "Qwen/Qwen3-Embedding-0.6B",
		Dimensions:       1024,
		StaleDocuments:   18,
		PendingDocuments: 2,
	}}
	controller := newReembedController(t, svc)

	ctx, recorder := reembedContext(t, http.MethodGet)
	controller.EmbeddingStatus(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", recorder.Code)
	}
	env := decodeEnvelope(t, recorder)
	if env.Code != apperrors.CodeSuccess {
		t.Fatalf("信封 code = %d，期望成功", env.Code)
	}
	var got responsedto.KnowledgeEmbeddingStatus
	if err := json.Unmarshal(env.Data, &got); err != nil {
		t.Fatalf("解析 data 失败: %v", err)
	}
	if got.StaleDocuments != 18 || got.PendingDocuments != 2 || got.Model != "Qwen/Qwen3-Embedding-0.6B" || got.Dimensions != 1024 {
		t.Errorf("体检结果不对: %+v", got)
	}
}

// 批量入队的结果带计数；部分入队（队列满）仍是 200 —— 已排队的那部分已经生效。
func TestReembedReturnsPartialCounts(t *testing.T) {
	svc := &stubReembedService{result: responsedto.KnowledgeReembedResult{
		Total: 18, Queued: 15, Skipped: 0, QueueFull: true,
	}}
	controller := newReembedController(t, svc)

	ctx, recorder := reembedContext(t, http.MethodPost)
	controller.Reembed(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", recorder.Code)
	}
	env := decodeEnvelope(t, recorder)
	if env.Code != apperrors.CodeSuccess {
		t.Fatalf("信封 code = %d，期望成功", env.Code)
	}
	var got responsedto.KnowledgeReembedResult
	if err := json.Unmarshal(env.Data, &got); err != nil {
		t.Fatalf("解析 data 失败: %v", err)
	}
	if got.Total != 18 || got.Queued != 15 || !got.QueueFull {
		t.Errorf("计数不对: %+v", got)
	}
}

// "现在不行"的两种状态给信封 code 409：队列已满、还没配置默认模型。
// HTTP 状态按项目约定仍是 200（业务错误看信封 code），与 Retry 接口同一套口径。
func TestReembedMapsStateErrorsToConflict(t *testing.T) {
	cases := map[string]error{
		"队列已满":   service.ErrIngestQueueFull,
		"没有默认模型": service.ErrNoEmbeddingModel,
	}
	for name, cause := range cases {
		t.Run(name, func(t *testing.T) {
			controller := newReembedController(t, &stubReembedService{err: cause})

			ctx, recorder := reembedContext(t, http.MethodPost)
			controller.Reembed(ctx)

			env := decodeEnvelope(t, recorder)
			if env.Code != apperrors.CodeConflict {
				t.Fatalf("信封 code = %d，期望 %d（%s）", env.Code, apperrors.CodeConflict, name)
			}
		})
	}
}

// 其余错误（数据库/事务故障）是内部错误，信封 code 翻 500 而不是 409/400。
func TestReembedMapsUnknownErrorToInternal(t *testing.T) {
	controller := newReembedController(t, &stubReembedService{err: errors.New("数据库连接失败")})

	ctx, recorder := reembedContext(t, http.MethodPost)
	controller.Reembed(ctx)

	env := decodeEnvelope(t, recorder)
	if env.Code != apperrors.CodeInternalError {
		t.Fatalf("信封 code = %d，期望 %d", env.Code, apperrors.CodeInternalError)
	}
}
