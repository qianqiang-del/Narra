package voice

import (
	"encoding/base64"

	dto "narra/internal/model/dto/response"
	"narra/internal/service"
	"narra/pkg/response"

	"github.com/gin-gonic/gin"
)

// Controller 音色目录接口。
type Controller struct {
	svc service.VoiceService
}

// NewController 创建音色控制器。
func NewController(svc service.VoiceService) *Controller {
	return &Controller{svc: svc}
}

// List 获取音色目录。
//
//	GET /api/v1/voices
func (c *Controller) List(ctx *gin.Context) {
	items, err := c.svc.List(ctx.Request.Context())
	if err != nil {
		response.BizError(ctx, err)
		return
	}

	response.Success(ctx, items)
}

// Preview 试听音色，合成一句固定的文案。
//
//	GET /api/v1/voices/:id/preview
//
// 音频 base64 之后塞进常规 JSON 信封，而不是返回裸字节流：整个后端只有一套信封，
// 前端只有一个 request() 会解它。为一个接口破例，前端就得多一套 fetch + blob 处理
// 和一整套错误分支。试听文案只有十来个字，base64 那 33% 的膨胀可以忽略。
func (c *Controller) Preview(ctx *gin.Context) {
	voiceID := ctx.Param("id")

	audio, err := c.svc.Preview(ctx.Request.Context(), voiceID)
	if err != nil {
		response.BizError(ctx, err)
		return
	}

	response.Success(ctx, dto.VoicePreview{
		VoiceID: voiceID,
		Format:  "wav",
		Audio:   base64.StdEncoding.EncodeToString(audio),
	})
}
