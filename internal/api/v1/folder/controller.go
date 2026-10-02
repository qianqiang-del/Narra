package folder

import (
	"strconv"

	"github.com/gin-gonic/gin"

	requestdto "narra/internal/model/dto/request"
	"narra/internal/service"
	"narra/pkg/response"
)

type Controller struct{ svc service.FolderService }

func NewController(svc service.FolderService) *Controller { return &Controller{svc: svc} }

func (c *Controller) Create(ctx *gin.Context) {
	var input requestdto.Folder
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

func (c *Controller) List(ctx *gin.Context) {
	items, err := c.svc.List(ctx.Request.Context())
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, items)
}

func (c *Controller) Get(ctx *gin.Context) {
	id, ok := parseID(ctx, "id")
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

func (c *Controller) Rename(ctx *gin.Context) {
	id, ok := parseID(ctx, "id")
	if !ok {
		return
	}
	var input requestdto.Folder
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}
	item, err := c.svc.Rename(ctx.Request.Context(), id, input)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, item)
}

func (c *Controller) Delete(ctx *gin.Context) {
	id, ok := parseID(ctx, "id")
	if !ok {
		return
	}
	if err := c.svc.Delete(ctx.Request.Context(), id); err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, gin.H{"id": id})
}

func (c *Controller) AddClassroom(ctx *gin.Context) {
	folderID, classroomID, ok := parseMembershipIDs(ctx)
	if !ok {
		return
	}
	if err := c.svc.AddClassroom(ctx.Request.Context(), folderID, classroomID); err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, gin.H{"folder_id": folderID, "classroom_id": classroomID})
}

func (c *Controller) RemoveClassroom(ctx *gin.Context) {
	folderID, classroomID, ok := parseMembershipIDs(ctx)
	if !ok {
		return
	}
	if err := c.svc.RemoveClassroom(ctx.Request.Context(), folderID, classroomID); err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, gin.H{"folder_id": folderID, "classroom_id": classroomID})
}

func parseMembershipIDs(ctx *gin.Context) (uint64, uint64, bool) {
	folderID, ok := parseID(ctx, "id")
	if !ok {
		return 0, 0, false
	}
	classroomID, ok := parseID(ctx, "classroom_id")
	return folderID, classroomID, ok
}

func parseID(ctx *gin.Context, name string) (uint64, bool) {
	id, err := strconv.ParseUint(ctx.Param(name), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(ctx, "无效的 ID")
		return 0, false
	}
	return id, true
}
