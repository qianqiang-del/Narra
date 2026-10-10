package auth

import (
	"errors"

	"narra/internal/middleware"
	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
	"narra/internal/service"
	"narra/pkg/response"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Controller struct{ svc service.AuthService }

func NewController(svc service.AuthService) *Controller { return &Controller{svc: svc} }

func (c *Controller) SendCode(ctx *gin.Context) {
	var input requestdto.AuthCode
	if ctx.ShouldBindJSON(&input) != nil {
		response.BadRequest(ctx, "手机号和验证码用途不能为空")
		return
	}
	if err := c.svc.SendCode(ctx.Request.Context(), input.Phone, input.Purpose, ctx.ClientIP()); err != nil {
		respondError(ctx, err)
		return
	}
	response.Success(ctx, responsedto.AuthCodeSent{Sent: true})
}

func (c *Controller) Register(ctx *gin.Context) {
	var input requestdto.AuthRegister
	if ctx.ShouldBindJSON(&input) != nil {
		response.BadRequest(ctx, "手机号、密码和验证码不能为空")
		return
	}
	result, err := c.svc.Register(ctx.Request.Context(), input.Phone, input.Password, input.Code)
	if err != nil {
		respondError(ctx, err)
		return
	}
	response.Success(ctx, result)
}

func (c *Controller) LoginPassword(ctx *gin.Context) {
	var input requestdto.AuthPasswordLogin
	if ctx.ShouldBindJSON(&input) != nil {
		response.BadRequest(ctx, "手机号和密码不能为空")
		return
	}
	result, err := c.svc.LoginPassword(ctx.Request.Context(), input.Phone, input.Password)
	if err != nil {
		respondError(ctx, err)
		return
	}
	response.Success(ctx, result)
}

func (c *Controller) LoginCode(ctx *gin.Context) {
	var input requestdto.AuthCodeLogin
	if ctx.ShouldBindJSON(&input) != nil {
		response.BadRequest(ctx, "手机号和验证码不能为空")
		return
	}
	result, err := c.svc.LoginCode(ctx.Request.Context(), input.Phone, input.Code)
	if err != nil {
		respondError(ctx, err)
		return
	}
	response.Success(ctx, result)
}

func (c *Controller) Me(ctx *gin.Context) {
	user, err := c.svc.GetUser(ctx.Request.Context(), middleware.GetUserID(ctx))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Unauthorized(ctx, "账号不存在")
		} else {
			response.InternalError(ctx, "查询账号失败")
		}
		return
	}
	response.Success(ctx, user)
}

func respondError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidPhone), errors.Is(err, service.ErrInvalidPassword), errors.Is(err, service.ErrInvalidCode):
		response.BadRequest(ctx, err.Error())
	case errors.Is(err, service.ErrAccountExists):
		response.Conflict(ctx, err.Error())
	case errors.Is(err, service.ErrAccountNotFound):
		response.NotFound(ctx, err.Error())
	case errors.Is(err, service.ErrInvalidCredentials):
		response.Unauthorized(ctx, err.Error())
	case errors.Is(err, service.ErrCodeRateLimited):
		response.Error(ctx, 429, err.Error())
	case errors.Is(err, service.ErrSMSUnavailable):
		response.Error(ctx, 503, err.Error())
	default:
		response.InternalError(ctx, "账号服务暂不可用")
	}
}
