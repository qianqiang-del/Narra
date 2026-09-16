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

// GET /api/v1/voices/:id/preview
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
