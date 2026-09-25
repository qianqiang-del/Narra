package discussion

import (
	"strconv"

	"github.com/gin-gonic/gin"

	requestdto "narra/internal/model/dto/request"
	"narra/internal/service"
	"narra/pkg/response"
)

// Controller 是多 Agent 讨论的触发入口。
//
// 它只做三件事：解析参数、把活交给业务层、把结果写成响应。不拼角色、不查库、
// 也不在这里等讨论跑完。
type Controller struct {
	svc service.DiscussionService
}

// NewController 创建讨论入口处理器。
func NewController(svc service.DiscussionService) *Controller {
	return &Controller{svc: svc}
}

// Start 在一条对话里发一句话，触发一趟多 Agent 讨论。
//
//	POST /api/v1/conversations/:id/discussions
//
// 受理即返回（回统一信封的 200，数据是对话 ID 与消息 ID）：
// 讨论在后台跑，它的过程与结果通过 GET /api/v1/conversations/:id/events 边跑边看。
//
// 为什么不在这里等讨论跑完：一趟讨论要按角色数调用好几次大模型，几十秒起步。
// 同步等的结果是请求超时、以及用户全程看不到任何进展 —— 而"边跑边看"正是
// 事件流那套东西存在的理由。
func (c *Controller) Start(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(ctx, "对话 ID 无效")
		return
	}

	var input requestdto.StartDiscussion
	if err := ctx.ShouldBindJSON(&input); err != nil {
		// 正文由业务层校验：那边能给出"内容不能为空 / 超过多少字"这种具体的话，
		// 这里只挡住"body 根本不是 JSON"。
		response.BadRequest(ctx, "请求体格式错误")
		return
	}

	result, err := c.svc.Start(ctx.Request.Context(), id, input.Content)
	if err != nil {
		response.BizError(ctx, err)
		return
	}

	response.Success(ctx, result)
}
