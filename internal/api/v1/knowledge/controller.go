package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	requestdto "narra/internal/model/dto/request"
	"narra/internal/model/entity"
	"narra/internal/service"
	"narra/pkg/documentparser"
	"narra/pkg/response"
)

const (
	// uploadFieldName 是上传表单里文件字段的名字。
	uploadFieldName = "file"

	// maxUploadBytes 单次上传的文件大小上限。
	//
	// 16 MB 是照着"知识文档"定的：几万字的 PDF 或 Word 通常远小于它。
	// 设上限的意义是让"传错文件"尽早失败：收录已经异步（不再受 HTTP 写超时约束），
	// 但一个几百兆的文件仍会占住暂存区磁盘，并在切分与向量化上白烧上游额度 ——
	// 那种失败要等几分钟才在列表里变成 failed，不如在这里当场拒掉。
	maxUploadBytes = 16 << 20

	// uploadTempPrefix 上传文件的临时目录前缀。
	uploadTempPrefix = "narra-upload-"
)

// Controller 负责知识库文档接口。
type Controller struct {
	svc       service.KnowledgeService
	uploadDir string
	parser    documentparser.Parser
}

func NewController(svc service.KnowledgeService, uploadDir string, parser documentparser.Parser) *Controller {
	return &Controller{svc: svc, uploadDir: uploadDir, parser: parser}
}

func (c *Controller) ParserStatus(ctx *gin.Context) {
	if c.parser == nil {
		response.Success(ctx, documentparser.Status{Ready: false, Reason: "document parser is disabled"})
		return
	}
	response.Success(ctx, c.parser.Status(ctx.Request.Context()))
}

// Upload 接收一个文件，登记为待收录文档后立即返回。
//
// 返回时文档是 pending：请求只做落盘与建行，解析与向量化由 rag.Worker 在后台推进。
// 调用方拿响应里的 id 轮询 GET /knowledge/documents/:id 看进度。
func (c *Controller) Upload(ctx *gin.Context) {
	header, err := ctx.FormFile(uploadFieldName)
	if err != nil {
		response.BadRequest(ctx, "请通过 "+uploadFieldName+" 字段上传文件")
		return
	}
	if header.Size > maxUploadBytes {
		response.BadRequest(ctx, fmt.Sprintf(
			"文件大小 %d MB 超过上限 %d MB", header.Size>>20, maxUploadBytes>>20))
		return
	}

	directory := filepath.Join(c.uploadDir, "pending", fmt.Sprintf("%d", time.Now().UnixNano()))
	err = os.MkdirAll(directory, 0o755)
	if err != nil {
		response.InternalError(ctx, "创建上传临时目录失败: "+err.Error())
		return
	}
	// 收录过程里的解析产物和这份上传文件都在这个目录下，一次清理干净。
	// 解析器的产物目录由 service 负责删，这里只管自己建的这个。

	// 落盘用的文件名不沿用用户给的名字：那个名字可能带 ../ 或盘符，
	// 拼进路径等于把"往任意位置写文件"的能力交给了调用方。
	// 但后缀必须保留 —— 解析器正是按后缀选实现的。
	accepted := false
	defer func() {
		if !accepted {
			_ = os.RemoveAll(directory)
		}
	}()
	path := filepath.Join(directory, "upload"+safeExtension(header.Filename))
	if err := ctx.SaveUploadedFile(header, path); err != nil {
		response.InternalError(ctx, "保存上传文件失败: "+err.Error())
		return
	}

	document, err := c.svc.SubmitFile(ctx.Request.Context(), requestdto.KnowledgeIngestFile{
		Path:       path,
		Title:      ctx.PostForm("title"),
		SourceType: entity.KnowledgeDocumentSourceImport,
		// 来源标识用用户看到的原始文件名，而不是服务器上的临时路径。
		SourceURI: filepath.Base(header.Filename),
	})
	accepted = err == nil
	if err != nil {
		response.BadRequest(ctx, err.Error())
		return
	}
	response.SuccessWithMessage(ctx, "文档已收录", document)
}

// IngestText 直接把一段正文收录为知识文档。
//
// 提供它是为了让"不走文件"的场景也能用同一条链路（外部系统同步、编辑器保存），
// 同时它也把整条链路的输入缩到最短 —— 排查问题时用一条 curl 就能跑完。
func (c *Controller) IngestText(ctx *gin.Context) {
	var input requestdto.KnowledgeIngestText
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "请求格式无效，需要 title 和 content")
		return
	}

	document, err := c.svc.IngestText(ctx.Request.Context(), input)
	if err != nil {
		response.BadRequest(ctx, err.Error())
		return
	}
	response.SuccessWithMessage(ctx, "文档已收录", document)
}

// List 分页返回文档列表。
func (c *Controller) List(ctx *gin.Context) {
	page, size := service.NormalizePage(queryInt(ctx, "page", 1), queryInt(ctx, "size", 20))

	documents, total, err := c.svc.List(ctx.Request.Context(), page, size)
	if err != nil {
		response.InternalError(ctx, err.Error())
		return
	}
	response.Success(ctx, response.NewPageResponse(documents, total, page, size))
}

// Get 返回单篇文档。
func (c *Controller) Get(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(ctx, "文档 ID 无效")
		return
	}

	document, err := c.svc.Get(ctx.Request.Context(), id)
	if err != nil {
		response.BadRequest(ctx, err.Error())
		return
	}
	response.Success(ctx, document)
}

func (c *Controller) Preview(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(ctx, "文档 ID 无效")
		return
	}
	document, err := c.svc.Preview(ctx.Request.Context(), id)
	if err != nil {
		response.BadRequest(ctx, err.Error())
		return
	}
	response.Success(ctx, document)
}

func (c *Controller) Delete(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(ctx, "文档 ID 无效")
		return
	}
	if err := c.svc.Delete(ctx.Request.Context(), id); err != nil {
		response.BadRequest(ctx, err.Error())
		return
	}
	response.SuccessWithMessage(ctx, "文档已删除", nil)
}

// queryInt 读一个整数查询参数，缺失或非法时用默认值。
// 分页参数给默认值比报错合适：列表页第一次打开本来就不会带上它们。
func queryInt(ctx *gin.Context, name string, fallback int) int {
	raw := strings.TrimSpace(ctx.Query(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

// safeExtension 取文件后缀，并且只保留字母数字。
//
// 后缀必须保留（解析器按它选实现），但也不能原样用：用户给的文件名里
// 可能带空格、引号甚至路径分隔符，拼进临时路径会失败或写到别处。
// 后缀本来就不该有这些字符，过滤掉说明这个文件名本身有问题，此时返回空串，
// 让"没有能解析 xx 的解析器"这句错误来收场，比在临时路径上出岔子清楚。
func safeExtension(name string) string {
	extension := strings.ToLower(filepath.Ext(filepath.Base(name)))
	if len(extension) < 2 || len(extension) > 11 {
		return ""
	}
	for _, symbol := range extension[1:] {
		isLetter := symbol >= 'a' && symbol <= 'z'
		isDigit := symbol >= '0' && symbol <= '9'
		if !isLetter && !isDigit {
			return ""
		}
	}
	return extension
}
