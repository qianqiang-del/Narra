package knowledge

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
	"narra/internal/service"
	"narra/pkg/config"
	"narra/pkg/documentparser"
	"narra/pkg/logger"
	"narra/pkg/response"
	"narra/pkg/sse"
)

const (
	// uploadFieldPlural 是批量上传的文件字段名；uploadFieldSingular 是旧契约的单文件字段名。
	// 两个都接受：新前端用 files（哪怕只传一个），老调用方继续用 file 并按老形状收响应。
	uploadFieldPlural   = "files"
	uploadFieldSingular = "file"

	// uploadDirAttempts 是随机暂存目录名碰撞后的重试次数。
	// 16 字节随机数的碰撞概率可以忽略，重试只是给"随机源或文件系统异常"留一条明确失败路径。
	uploadDirAttempts = 3

	// multipartMemory 是 multipart 解析时留在内存里的上限，超出部分由标准库落系统临时目录。
	// 32MB 与 gin 的默认值一致：单文件上限 16MB，一批的头部与内存占用都在可控范围。
	multipartMemory = 32 << 20

	// 收录进度流（GET /knowledge/documents/:id/events）上的事件名，
	// 与前端 api/knowledge.ts 的 watchKnowledgeDocument 一一对应。
	eventDocument = "document" // 文档快照，形状同 GET /knowledge/documents/:id
	eventParser   = "parser"   // 解析环境状态，形状同 GET /knowledge/documents/parser/status
	eventError    = "error"    // 流中途失败（例如文档被删），推完即关流

	// eventsQueryInterval 是进度流内部查库的节奏，与前端原来的轮询一致：
	// 换掉的是每秒一次的 HTTP 往返与请求日志，查询本身的代价不变。
	eventsQueryInterval = time.Second

	// eventsHeartbeatInterval 心跳间隔。空闲连接会被反代与浏览器掐掉，
	// 每 20 秒报一次活；写失败也正好是"对端已断开"的检测点。
	eventsHeartbeatInterval = 20 * time.Second

	// eventsMaxDuration 是服务端兜底的最长推送时长：客户端异常（不关连接也不再读）
	// 时不留一条永远跑下去的循环。正常路径走不到它 —— 文档到终态即关流，
	// 前端自己也有 15 分钟的处理超时（见 frontend/src/stores/knowledge.ts）。
	eventsMaxDuration = 45 * time.Minute
)

// Controller 是知识库文档的 HTTP 处理器。
//
// 它只管三件与传输有关的事：收文件并落盘、把请求参数翻成服务层输入、
// 把服务层结果装进统一响应体。收录本身的编排在 service 与 rag 里。
type Controller struct {
	svc       service.KnowledgeService
	uploadDir string                // 上传根目录；每次上传在此新建一个单独的暂存子目录
	parser    documentparser.Parser // 可以为 nil，表示文档解析能力未启用
	limits    config.KnowledgeIngestConfig

	// 进度流（Events）的节奏参数。零值由 NewController 补成 events* 默认值；
	// 测试调小它们，否则一条状态流转要等秒级才能观察完。
	eventsQueryInterval     time.Duration
	eventsHeartbeatInterval time.Duration
	eventsMaxDuration       time.Duration
}

// NewController 创建知识库处理器。
//
// uploadDir 要和服务层、worker 用的是同一个值：服务层靠它判断一条记录的
// upload_path 是否可信（只清理自己目录下的），传错会让删除时的清理失效。
//
// limits 是批量上传与队列的上限，直接来自配置；零值会被补成默认值，见 WithDefaults。
func NewController(svc service.KnowledgeService, uploadDir string, parser documentparser.Parser, limits config.KnowledgeIngestConfig) *Controller {
	return &Controller{
		svc:                     svc,
		uploadDir:               uploadDir,
		parser:                  parser,
		limits:                  limits.WithDefaults(),
		eventsQueryInterval:     eventsQueryInterval,
		eventsHeartbeatInterval: eventsHeartbeatInterval,
		eventsMaxDuration:       eventsMaxDuration,
	}
}

// ParserStatus 返回文档解析能力的可用状态，供前端提示"当前能不能传 PDF"。
//
// parser 为 nil 是"配置上就没启用"的确定状态，直接答一个 ready = false；
// 非 nil 时交给解析器自己探测（例如 Python 运行时和依赖是否就绪）。
func (c *Controller) ParserStatus(ctx *gin.Context) {
	response.Success(ctx, c.parserSnapshot(ctx.Request.Context()))
}

// parserSnapshot 取解析能力状态快照，parser 未启用时给出确定状态。
//
// ParserStatus 与进度流共用它：两边对"没启用"的说法必须是同一句，
// 否则同一个界面会从接口和流里拿到两种解释。
func (c *Controller) parserSnapshot(ctx context.Context) documentparser.Status {
	if c.parser == nil {
		// Enabled=false 与"没准备好"是两件事：前者是配置里就没开，
		// 前端据此提示的是"这类文件暂时没法解析"，而不是"首次上传要等一会儿"。
		return documentparser.Status{Enabled: false, Ready: false, Reason: "document parser is disabled"}
	}
	return c.parser.Status(ctx)
}

// Upload 接收 1..N 个文件，逐个登记为待收录文档后立即返回。
//
// 返回时文档都是 pending：请求只做落盘与建行，解析与向量化由 rag.Worker 在后台推进。
// 批量响应（HTTP 202）逐项给出 document_id / status / error，调用方对已入队的项
// 订阅 GET /knowledge/documents/:id/events 看进度，或轮询上传记录列表。
//
// 字段与形状：
//   - 新契约用 files（可重复）——逐项结果的批量响应；
//   - 旧契约的单 file 字段仍然接受，按原来的形状返回文档对象（HTTP 200），
//     避免新老前端在同一个部署里对不上。两条路复用同一段落盘与提交逻辑。
//
// 逐文件结果与整批失败的边界：
//   - 单文件超限、空文件、队列已满、写库失败 → 只有那一项是 rejected，其余照常入队；
//   - 请求体超限、multipart 损坏、文件数超过上限 → 整批失败（这时还没有安全地
//     枚举出文件列表，给不出逐项结果）。
func (c *Controller) Upload(ctx *gin.Context) {
	files, legacy, ok := c.readUploadForm(ctx)
	if !ok {
		return
	}

	// title 是单文件上传的字段；批量时它没有"这一份文件"的语义，忽略。
	// 单文件时哪怕走新字段 files 也照常生效。
	title := ""
	if len(files) == 1 {
		title = ctx.PostForm("title")
	}

	items := make([]responsedto.KnowledgeIngestItem, 0, len(files))
	documents := make([]*responsedto.KnowledgeDocument, 0, len(files))
	errs := make([]error, 0, len(files))
	for _, header := range files {
		item, document, err := c.submitUpload(ctx, header, title)
		items = append(items, item)
		documents = append(documents, document)
		errs = append(errs, err)
	}

	if legacy {
		c.respondLegacyUpload(ctx, items[0], documents[0], errs[0])
		return
	}
	response.Accepted(ctx, "已受理", summarizeIngestBatch(items))
}

// readUploadForm 解析 multipart，返回待处理的文件列表。
//
// 三道读取侧约束的顺序是有讲究的：
//  1. Content-Length 预检：浏览器上传表单一定带它，明显超限时在读任何 body 之前拒绝，
//     客户端能拿到干净的 JSON 错误；没有 Content-Length 的 chunked 请求落到第 2 步。
//  2. http.MaxBytesReader 包住 body：这是真正的硬边界 —— 它包含 multipart 边界与
//     分段的头部开销，不能只按文件总大小算（见 config.BodyLimitBytes）。超限时
//     net/http 会标记连接并在响应后关闭它，chunked 客户端可能看到连接被重置而不是
//     这条 JSON 错误，这是"拒绝继续收 body"的固有代价。
//  3. 解析成功之后才检查文件数：数量超限也只能整批拒绝，因为"取前 N 个、丢其余"
//     等于替用户做了丢弃决定。
//
// 返回的 legacy 表示请求用的是旧的 file 字段，调用方据此选响应形状。
func (c *Controller) readUploadForm(ctx *gin.Context) (files []*multipart.FileHeader, legacy bool, ok bool) {
	bodyLimit := c.limits.BodyLimitBytes()
	if ctx.Request.ContentLength > bodyLimit {
		response.BadRequest(ctx, fmt.Sprintf(
			"请求体 %d MB 超过单次批量上限 %d MB",
			ctx.Request.ContentLength>>20, c.limits.MaxBatchBytes>>20))
		return nil, false, false
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, bodyLimit)

	if err := ctx.Request.ParseMultipartForm(multipartMemory); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			response.BadRequest(ctx, fmt.Sprintf(
				"请求体超过单次批量上限 %d MB（含 multipart 开销）", c.limits.MaxBatchBytes>>20))
			return nil, false, false
		}
		response.BadRequest(ctx, "请求格式无效，请用 multipart/form-data 上传文件")
		return nil, false, false
	}

	files = ctx.Request.MultipartForm.File[uploadFieldPlural]
	if len(files) == 0 {
		files = ctx.Request.MultipartForm.File[uploadFieldSingular]
		legacy = true
	}
	if len(files) == 0 {
		response.BadRequest(ctx, "请通过 "+uploadFieldPlural+" 字段上传文件")
		return nil, false, false
	}
	if len(files) > c.limits.MaxFiles {
		response.BadRequest(ctx, fmt.Sprintf(
			"一次最多上传 %d 个文件，本次 %d 个", c.limits.MaxFiles, len(files)))
		return nil, false, false
	}
	return files, legacy, true
}

// submitUpload 处理一个上传文件：校验 → 落盘 → 提交收录。
//
// 返回的 err 仅供旧单文件契约区分 409 与 400；批量路径只用 item 里的 status / error。
// 无论成功与否，只要落过盘就会在失败时把暂存目录删掉 —— worker 从没见过它，
// 不删就是一份永远没人认领的孤儿文件。
func (c *Controller) submitUpload(
	ctx *gin.Context,
	header *multipart.FileHeader,
	title string,
) (responsedto.KnowledgeIngestItem, *responsedto.KnowledgeDocument, error) {
	item := responsedto.KnowledgeIngestItem{
		OriginalName: filepath.Base(header.Filename),
		Status:       responsedto.KnowledgeIngestItemStatusRejected,
	}

	switch {
	case header.Size <= 0:
		err := fmt.Errorf("文件内容为空")
		item.Error = err.Error()
		return item, nil, err
	case header.Size > c.limits.MaxFileBytes:
		err := fmt.Errorf("文件大小 %d MB 超过上限 %d MB",
			header.Size>>20, c.limits.MaxFileBytes>>20)
		item.Error = err.Error()
		return item, nil, err
	}

	path, err := c.stageUpload(ctx, header)
	if err != nil {
		err = fmt.Errorf("保存上传文件失败: %w", err)
		item.Error = err.Error()
		return item, nil, err
	}

	document, err := c.svc.SubmitFile(ctx.Request.Context(), requestdto.KnowledgeIngestFile{
		Path:       path,
		Title:      title,
		SourceType: entity.KnowledgeDocumentSourceImport,
		// 来源标识用用户看到的原始文件名，而不是服务器上的临时路径。
		SourceURI: filepath.Base(header.Filename),
		// 字节数取自接收到的文件头，是这份文件在上传记录里唯一能显示的大小信息。
		SizeBytes: header.Size,
	})
	if err != nil {
		// 提交失败：原件已经落盘，删掉它。worker 从来没见过这份文件，
		// 不删就是一份没有数据库行认领的孤儿。
		c.discardUpload(path)
		item.Error = err.Error()
		return item, nil, err
	}

	documentID := document.ID
	item.DocumentID = &documentID
	item.Status = responsedto.KnowledgeIngestItemStatusPending
	return item, &document, nil
}

// respondLegacyUpload 用旧契约回单文件上传：成功返回文档对象（HTTP 200），
// 失败按原因翻成 409 / 400 —— 与批量接口的 202 + 逐项结果刻意不同，
// 只为让还没升级的调用方行为不变。
func (c *Controller) respondLegacyUpload(
	ctx *gin.Context,
	item responsedto.KnowledgeIngestItem,
	document *responsedto.KnowledgeDocument,
	err error,
) {
	if document != nil {
		response.SuccessWithMessage(ctx, "文档已收录", document)
		return
	}
	if errors.Is(err, service.ErrIngestQueueFull) {
		response.Conflict(ctx, item.Error)
		return
	}
	response.BadRequest(ctx, item.Error)
}

// summarizeIngestBatch 把逐项结果折成带汇总的批量响应。
func summarizeIngestBatch(items []responsedto.KnowledgeIngestItem) responsedto.KnowledgeIngestBatch {
	accepted := 0
	for _, item := range items {
		if item.Status == responsedto.KnowledgeIngestItemStatusPending {
			accepted++
		}
	}
	return responsedto.KnowledgeIngestBatch{
		Items:    items,
		Accepted: accepted,
		Rejected: len(items) - accepted,
	}
}

// stageUpload 把一个上传文件落到独立随机目录里，返回最终路径。
//
// 目录名用 crypto/rand 而不是时间戳：批量上传会在极短时间内连续落盘多个文件，
// 时间戳可能落在同一系统时钟粒度内；而 os.MkdirAll 对已存在目录不报错 ——
// 碰撞的后果是两个文件静默共用一个目录、后写的覆盖前者。os.Mkdir 的 EEXIST
// 会把碰撞变成显式错误，这里重试几次仍失败才放弃。
//
// 文件名必须由服务端固定，不能沿用用户给的名字：那个名字可能带 ../ 或盘符，
// 拼进路径等于把"往任意位置写文件"的能力交给调用方。但后缀要保留 ——
// 解析器正是按后缀选实现的（见 safeExtension）。
//
// 落盘后核对实际字节数：SaveUploadedFile 不检查 Close 的写入错误，磁盘写满时
// 可能留下一个被截断的文件，而它一旦建了 pending 行就会被 worker 拿去解析。
func (c *Controller) stageUpload(ctx *gin.Context, header *multipart.FileHeader) (string, error) {
	if err := os.MkdirAll(c.pendingRoot(), 0o755); err != nil {
		return "", err
	}

	var lastErr error
	for attempt := 0; attempt < uploadDirAttempts; attempt++ {
		name, err := randomUploadDirName()
		if err != nil {
			return "", err
		}
		directory := filepath.Join(c.pendingRoot(), name)
		if err := os.Mkdir(directory, 0o755); err != nil {
			if errors.Is(err, fs.ErrExist) {
				lastErr = err
				continue
			}
			return "", err
		}

		path := filepath.Join(directory, "upload"+safeExtension(header.Filename))
		if err := ctx.SaveUploadedFile(header, path); err != nil {
			_ = os.RemoveAll(directory)
			return "", err
		}
		info, err := os.Stat(path)
		if err != nil {
			_ = os.RemoveAll(directory)
			return "", err
		}
		if info.Size() != header.Size {
			_ = os.RemoveAll(directory)
			return "", fmt.Errorf("写入 %d 字节，与上传声明的大小 %d 不一致", info.Size(), header.Size)
		}
		return path, nil
	}
	return "", fmt.Errorf("生成暂存目录名多次冲突: %w", lastErr)
}

// discardUpload 删掉一份已经落盘但没能入队的原件，失败时留 warning。
//
// 删除失败在 Windows 上并不罕见（文件仍被占用），而这份文件没有数据库行指向它，
// worker 的清理路径永远看不到它：静默吞掉就是一个永远查不到原因的孤儿目录。
func (c *Controller) discardUpload(path string) {
	directory := filepath.Dir(path)
	if err := os.RemoveAll(directory); err != nil {
		logger.Warn("清理未入队文件的上传暂存目录失败，目录可能残留",
			zap.String("dir", directory),
			zap.Error(err),
		)
	}
}

// pendingRoot 是待处理原件的根目录，与 worker 的暂存目录约定一致。
func (c *Controller) pendingRoot() string {
	return filepath.Join(c.uploadDir, "pending")
}

// randomUploadDirName 生成 16 字节随机数的十六进制形式，用作暂存目录名。
func randomUploadDirName() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("生成暂存目录名失败: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// UploadLimits 返回批量上传的限制值，供前端在选择文件时预检。
//
// 服务端始终是唯一裁判：下发这份值不是为了让前端代替它判断，而是让"选完文件立刻
// 看到超限"不必先发一次注定失败的请求。配置改了这里就跟着变，前端无需同步发版。
func (c *Controller) UploadLimits(ctx *gin.Context) {
	response.Success(ctx, responsedto.KnowledgeUploadLimits{
		MaxFiles:      c.limits.MaxFiles,
		MaxFileBytes:  c.limits.MaxFileBytes,
		MaxBatchBytes: c.limits.MaxBatchBytes,
	})
}

// Events 用 SSE 推送一篇文档的收录进度，替掉前端每秒一次的详情轮询。
//
// 为什么是服务端轮询数据库，而不是让 rag.Worker 主动推：worker 在另一个包、
// 也不持有 HTTP 连接，让它发事件要多一层进程内总线；而这里每秒查一条文档的代价，
// 与前端原先每秒一次 GET 完全相同 —— 换掉的是往返、连接建立与请求日志。
// 将来若升级成事件驱动，这个接口对外的契约（事件名与帧的形状）可以保持不变。
//
// 契约（与前端 watchKnowledgeDocument 对齐）：
//   - 连上先推当前帧：一帧 document、一帧 parser；
//   - 之后有变化才推：文档变了推 document，解析环境进度变了推 parser；
//   - document 到终态（ready / failed）时推完最后一帧就关流；
//   - 中途取不到文档（例如用户删了这条记录）时推一帧 error 再关流。
//
// 它是一条"状态快照流"，不依赖事件回放：断线重连直接补当前帧，所以没有事件 id。
func (c *Controller) Events(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(ctx, "文档 ID 无效")
		return
	}

	requestCtx := ctx.Request.Context()
	// 首帧必须在 SSE 头之前取到：文档压根不存在时，调用方该收到的是统一信封的
	// JSON 错误，而不是一条"连上了但立刻报错"的事件流（响应头一出去就换不回来了）。
	document, err := c.svc.Get(requestCtx, id)
	if err != nil {
		response.BadRequest(ctx, err.Error())
		return
	}

	sse.Start(ctx)

	// 推帧用"序列化后比对"：progress 的 payload 就是写上线的那份字节，
	// 不依赖 DTO 字段的可比较性，以后加字段也不会漏推。
	lastDocument, _ := json.Marshal(document)
	lastParser, _ := json.Marshal(c.parserSnapshot(requestCtx))
	_ = sse.EventJSON(ctx, eventDocument, lastDocument)
	_ = sse.EventJSON(ctx, eventParser, lastParser)
	if isSettled(document.Status) {
		return
	}

	query := time.NewTicker(c.eventsQueryInterval)
	defer query.Stop()
	heartbeat := time.NewTicker(c.eventsHeartbeatInterval)
	defer heartbeat.Stop()
	deadline := time.NewTimer(c.eventsMaxDuration)
	defer deadline.Stop()

	for {
		select {
		case <-requestCtx.Done():
			// 客户端断开了（关页面、主动 abort）：结束循环，不用再写。
			return
		case <-deadline.C:
			_ = sse.Event(ctx, eventError, gin.H{"message": "进度推送已超时，请刷新查看最新状态"})
			return
		case <-heartbeat.C:
			if sse.Heartbeat(ctx) != nil {
				return
			}
		case <-query.C:
			document, err := c.svc.Get(requestCtx, id)
			if err != nil {
				_ = sse.Event(ctx, eventError, gin.H{"message": err.Error()})
				return
			}
			if payload, _ := json.Marshal(document); !bytes.Equal(payload, lastDocument) {
				lastDocument = payload
				if sse.EventJSON(ctx, eventDocument, payload) != nil {
					return
				}
			}
			if isSettled(document.Status) {
				return
			}
			if payload, _ := json.Marshal(c.parserSnapshot(requestCtx)); !bytes.Equal(payload, lastParser) {
				lastParser = payload
				if sse.EventJSON(ctx, eventParser, payload) != nil {
					return
				}
			}
		}
	}
}

// isSettled 判断文档是否到了终态。终态之后进度不会再变，流可以收了；
// 前端也据此停止等待（见 stores/knowledge.ts 的 isSettled）。
func isSettled(status string) bool {
	return status == entity.KnowledgeDocumentStatusReady || status == entity.KnowledgeDocumentStatusFailed
}

// Retry 把一条收录失败的文档重新排队，让后台拿同一份原件再跑一遍。
//
// 不收新文件：失败的原件已经被归档在服务器上（data/uploads/failed/<文档ID>/），
// 所以这次请求不需要 multipart，一个空 POST 就够。返回的文档是 pending，
// 调用方接着订阅 Events 看进度 —— 与上传之后的流程完全一样。
//
// 四种"现在不行"翻成 409 而不是 400：问题不在这次请求的参数，而在此刻的状态 ——
// 文档已经不是失败态、后台正忙着收别的、或者恢复所需的材料全都没了（原件、正文、
// 切片一个不剩，只能重新上传）。
func (c *Controller) Retry(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(ctx, "文档 ID 无效")
		return
	}

	document, err := c.svc.Retry(ctx.Request.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrIngestQueueFull),
			errors.Is(err, service.ErrRetryNotFailed),
			errors.Is(err, service.ErrRecoveryInputMissing):
			response.Conflict(ctx, err.Error())
		default:
			response.BadRequest(ctx, err.Error())
		}
		return
	}
	response.SuccessWithMessage(ctx, "已重新排队", document)
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
//
// 三个可选条件：status（可逗号分隔多个）、keyword、page / size；都不给就是
// "全部文档的第一页"。筛选在服务端做而不是拉回来再过滤，是因为列表本身就是分页的 ——
// 前端过滤只能看到已经拉下来的那几页，"第一页全是 failed、ready 排在第二页"
// 时会显示成空列表。
func (c *Controller) List(ctx *gin.Context) {
	query, err := service.ParseDocumentListQuery(
		queryInt(ctx, "page", 1),
		queryInt(ctx, "size", 20),
		ctx.Query("status"),
		ctx.Query("keyword"),
	)
	if err != nil {
		response.BadRequest(ctx, err.Error())
		return
	}

	documents, total, err := c.svc.List(ctx.Request.Context(), query)
	if err != nil {
		response.InternalError(ctx, err.Error())
		return
	}
	response.Success(ctx, response.NewPageResponse(documents, total, query.Page, query.Size))
}

// ListUploadRecords 分页返回上传记录（文件投递的历史流水）。
//
// 与 List 的区别在数据源：那边是文档（知识资产），这边是记录（投递动作）。
// 所以这里没有 status / keyword 筛选 —— 记录就是一条流水，按时间倒序列出来即可。
func (c *Controller) ListUploadRecords(ctx *gin.Context) {
	page, size := service.NormalizePage(queryInt(ctx, "page", 1), queryInt(ctx, "size", 20))

	records, total, err := c.svc.ListUploadRecords(ctx.Request.Context(), page, size)
	if err != nil {
		response.InternalError(ctx, err.Error())
		return
	}
	response.Success(ctx, response.NewPageResponse(records, total, page, size))
}

// DeleteUploadRecord 删除一条上传记录。
//
// 若它对应的文档还没收录成功，服务层会连同那份文档一起删（连带删除的判定在仓储里）。
// 已经收录成功的文档不受影响 —— 那种情况下只是这条投递历史消失了。
func (c *Controller) DeleteUploadRecord(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(ctx, "记录 ID 无效")
		return
	}
	if err := c.svc.DeleteUploadRecord(ctx.Request.Context(), id); err != nil {
		response.BadRequest(ctx, err.Error())
		return
	}
	response.SuccessWithMessage(ctx, "记录已删除", nil)
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

// SetEnabled 切换一篇文档是否参与检索。
//
// 停用不删任何东西：切片与向量都还在，只是召回时不再命中它；改回 true 立即恢复。
// 它对文档状态没有要求（字段与收录状态正交），界面上只在 ready 的行给入口。
func (c *Controller) SetEnabled(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(ctx, "文档 ID 无效")
		return
	}
	var input requestdto.KnowledgeDocumentEnabled
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "请求格式无效，需要 enabled")
		return
	}

	document, err := c.svc.SetEnabled(ctx.Request.Context(), id, input.Enabled)
	if err != nil {
		response.BadRequest(ctx, err.Error())
		return
	}
	response.SuccessWithMessage(ctx, "已更新检索状态", document)
}

// Preview 返回单篇文档的解析正文，供前端"查看"按钮打开预览。
//
// 正文只在这一个接口出网：列表与详情刻意不带它（一篇文档可能几十万字）。
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

// Delete 删除一篇文档，连同它的切片与向量。
//
// 三张表的级联由外键 ON DELETE CASCADE 保证，这里只发一次删除请求；
// 上传暂存目录的清理在服务层做（它要据此判断路径是否可信）。
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

// Retrieve 检索知识库，返回最相关的切片。
//
// 用 POST 而不是 GET：检索词是一句自然语言，塞进查询串会被长度与编码问题反复咬
// （长句、引号、& 都要转义），而 body 没有这些麻烦。它也不挂在 /documents 下面 ——
// 检索一次跨整库召回一批切片，命中的不是某一篇文档，与 documents / upload-records
// 是三组并列的资源。
//
// 错误分两类：参数类（检索词为空）翻 400，其余是检索链路自身的失败
// （向量服务没配好、两路召回都查不动）翻 500 —— 降级规则在 rag.Retriever 里，
// 能走到这里说明两条召回路都没给出结果，失败原因已经带上来了。
func (c *Controller) Retrieve(ctx *gin.Context) {
	var input requestdto.KnowledgeRetrieve
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "请求格式无效，需要 query")
		return
	}

	result, err := c.svc.Retrieve(ctx.Request.Context(), input)
	if err != nil {
		if errors.Is(err, service.ErrEmptyQuery) {
			response.BadRequest(ctx, err.Error())
			return
		}
		response.InternalError(ctx, err.Error())
		return
	}
	response.Success(ctx, result)
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
