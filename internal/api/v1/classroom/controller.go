package classroom

import (
	"strconv"

	requestdto "narra/internal/model/dto/request"
	"narra/internal/service"
	"narra/pkg/response"

	"github.com/gin-gonic/gin"
)

type Controller struct{ svc service.ClassroomService }

func NewController(svc service.ClassroomService) *Controller { return &Controller{svc: svc} }

func (c *Controller) Create(ctx *gin.Context) {
	var input requestdto.CreateClassroom
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}
	item, err := c.svc.Create(ctx.Request.Context(), input)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, item)
}

func (c *Controller) Get(ctx *gin.Context) {
	id, ok := parseID(ctx)
	if !ok {
		return
	}
	item, err := c.svc.Get(ctx.Request.Context(), id)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, item)
}

func parseID(ctx *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(ctx, "无效的 ID")
		return 0, false
	}
	return id, true
}
