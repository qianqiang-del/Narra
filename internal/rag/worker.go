package rag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"narra/internal/model/entity"
	"narra/pkg/logger"
)

// unusablePathReason 是"暂存文件不在自己管的上传目录里"时给用户的说明。
//
// 它同时写进两处：文档 metadata 的 error 键（失败现场）与上传记录的 error_message
// （界面直接显示的那一句）。共用同一个常量，免得两处说法漂移。
const unusablePathReason = "上传暂存文件不存在或路径无效"

// failedDirName 是上传根目录下归档失败原件的子目录名，一篇文档一个子目录，
// 名字就是它的文档 ID（failed/<文档ID>/upload.<ext>）。
//
// 与待处理目录（pending/）用纳秒时间戳命名不同，这里用文档 ID 是因为归档的文件
// 需要被反查：磁盘上捡到一份 upload.pptx，看目录名就知道它属于哪一篇、该不该清。
const failedDirName = "failed"

// 后台任务的节奏参数。刻意不做成配置：它们是"任务多久算死了"的内部一致性约束，
// 三个值互相咬合（心跳 < 判定阈值，扫描周期与心跳同量级），调错会直接导致误杀
// 或回收不及时。测试通过 Worker 上的同名字段缩短它们。
const (
	// defaultPollInterval 轮询 pending 的间隔。上传之后最多一秒就被接手。
	defaultPollInterval = time.Second

	// defaultHeartbeatEvery 处理期间推 updated_at 的间隔。
	defaultHeartbeatEvery = 30 * time.Second

	// defaultStaleAfter 超过这么久没有心跳的 processing 行会被打回 pending。
	//
	// 它只需要覆盖"心跳间隔 + DB 抖动 + 一轮调度的余量"，不需要覆盖最长任务 ——
	// 这正是心跳的意义：进程被 kill 后心跳立刻消失，3 分钟就能回收，
	// 而阈值不会误伤正在慢慢跑的长任务（它们一直在报到）。
	defaultStaleAfter = 3 * time.Minute

	// defaultStaleResetEvery 周期回收的执行间隔。
	defaultStaleResetEvery = time.Minute
)

// Worker 是文件收录的后台执行者。
//
// 它把"上传"和"解析"拆成两件事：HTTP 请求只负责落盘并建一条 pending 行
// （见 Ingester.SubmitFile），解析与向量化由这里按秒轮询 pending 行来推进。
// 这样一份需要几分钟的 PDF 不会再让请求撞上写超时，前端也能靠文档状态轮询进度。
//
// 队列就是 knowledge_documents 表本身，没有独立的任务表：ListPending 取行、
// ClaimAndReturnAttempt 抢行并领取租约编号、状态字段标记完成或失败。好处是任务与文档
// 同生共死 —— 删除文档不会留下孤儿任务，重启也不会丢队列。
//
// 处理是分阶段的：任务行上的 ingest_stage 记着下一步做什么，解析与切分的中间结果
// 各自落库。所以进程崩溃、向量服务抖动都不会让昂贵的解析白跑 —— 重新入队后从
// 失败的那一步继续（见 processOne 与 Ingester.processExistingFile）。
//
// concurrency 个常驻 runner 各自循环"抢一条 → 处理 → 再抢下一条"，互相之间没有
// 批次屏障：一个慢任务只占它自己那一份并发，不会让另一个空出来的并发位停工。
// 多实例部署时靠 ClaimAndReturnAttempt 的乐观更新保证同一行只被一个执行者拿到，
// 租约编号保证旧执行者的迟到写入会被挡掉。
type Worker struct {
	store       FileTaskStore
	ingester    *Ingester
	uploadRoot  string // 上传根目录（pending/ 待处理、failed/ 失败归档都在它下面）；只碰这个目录下的文件
	concurrency int

	// 节奏参数。零值由 NewWorker 补成 defaultXxx；测试可以调小它们，
	// 否则心跳与周期回收要跑分钟级才能观察到。
	pollInterval    time.Duration
	heartbeatEvery  time.Duration
	staleAfter      time.Duration
	staleResetEvery time.Duration

	// ctx 与 cancel 在构造时就建好，run 只负责使用。
	// 这样 Stop 与 run 之间不存在"谁先写"的竞争（旧实现里 cancel 是 run 赋值、Stop 读取）。
	ctx    context.Context
	cancel context.CancelFunc

	stop chan struct{}
	done chan struct{}
	once sync.Once

	// group 等 runner 与回收协程全部退出。Stop 通过 done 等它，不再需要额外的协调。
	group sync.WaitGroup
}

// NewWorker 创建 worker。concurrency 小于 1 时按 1 处理。
func NewWorker(store FileTaskStore, ingester *Ingester, uploadRoot string, concurrency int) *Worker {
	if concurrency < 1 {
		concurrency = 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Worker{
		store:           store,
		ingester:        ingester,
		uploadRoot:      uploadRoot,
		concurrency:     concurrency,
		pollInterval:    defaultPollInterval,
		heartbeatEvery:  defaultHeartbeatEvery,
		staleAfter:      defaultStaleAfter,
		staleResetEvery: defaultStaleResetEvery,
		ctx:             ctx,
		cancel:          cancel,
		stop:            make(chan struct{}),
		done:            make(chan struct{}),
	}
}

// Start 在后台协程里启动轮询循环，立即返回。
func (w *Worker) Start() {
	go w.run()
}

// Stop 停止轮询，并等待正在跑的任务收尾。
//
// 两步：close(stop) 让循环不再取新任务；cancel() 让正在解析或向量化的任务
// 从 ctx 上收到中断。被取消的任务不会落成 failed（见 Ingester.failIngest），
// 它会停在 processing，由周期 ResetStale 在下次启动后打回 pending 重跑。
//
// ctx 超时不算错误路径上的意外 —— 它只表示"没等完"：调用方应当把它当成
// "可能留下 processing 行"的告警，并且在那之后才关数据库。
// once 保证重复调用只关一次 channel。
func (w *Worker) Stop(ctx context.Context) error {
	w.once.Do(func() {
		close(w.stop)
		w.cancel()
	})
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run 启动常驻 runner 与周期回收协程，等它们全部退出后关闭 done。
//
// 回收独立成一个协程，而不是挂在"某一轮任务跑完之后"：一份 PDF 解析几十分钟很正常，
// 如果回收只在 runner 空闲时执行，一个崩溃进程留下的僵尸行要等任务全部结束才会被打回
// pending。回收只看 updated_at，与 runner 忙不忙无关。
//
// 启动时先清一次上一个进程留下的僵尸行（崩溃、被 kill 时它们永远停在 processing），
// 之后由 resetLoop 周期再清 —— 只做启动那一次是不够的：如果进程很快重启，那些刚被
// 更新过的行还"不够旧"，会被启动检查漏掉。
func (w *Worker) run() {
	defer close(w.done)

	_ = w.store.ResetStale(w.ctx, time.Now().Add(-w.staleAfter))

	w.group.Add(1)
	go w.resetLoop()

	for index := 0; index < w.concurrency; index++ {
		w.group.Add(1)
		go w.runner()
	}

	w.group.Wait()
}

// resetLoop 周期把心跳超时的 processing 行打回 pending。
func (w *Worker) resetLoop() {
	defer w.group.Done()

	ticker := time.NewTicker(w.staleResetEvery)
	defer ticker.Stop()

	for {
		select {
		case <-w.stop:
			return
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			_ = w.store.ResetStale(w.ctx, time.Now().Add(-w.staleAfter))
		}
	}
}

// runner 是一个常驻执行者：抢一条 pending 任务、处理完、再抢下一条。
//
// 不用"取一批、全部跑完再取下一批"：那样一个慢任务会拖住整批的调度，让已经空出来的
// 并发位干等。每条任务都是独立的 抢 → 处理 循环，慢任务只占它自己的那份并发。
// 抢任务的原子性由 ClaimAndReturnAttempt 的条件更新保证，多实例部署下同一行只会有一个
// 执行者。任务之间用一个 tick 的间隔轮询，空库时每秒一次的查询代价可以忽略。
func (w *Worker) runner() {
	defer w.group.Done()

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		w.runOnce(w.ctx)
		select {
		case <-w.stop:
			return
		case <-w.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// runOnce 抢一条 pending 任务并处理完再返回；没有可抢的任务时立即返回。
//
// 每个任务都要先抢再处理：ListPending 只是一次查询，多个 runner 或实例可能查到同一行。
// ClaimAndReturnAttempt 是一条带 status = 'pending' 条件的 UPDATE，谁把行改成
// processing 谁才算真的拿到任务，同时领到本次处理的租约编号。
func (w *Worker) runOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	documents, err := w.store.ListPending(ctx, 1)
	if err != nil {
		if ctx.Err() == nil {
			logger.Warn("取待处理文档失败，本轮跳过", zap.Error(err))
		}
		return
	}
	if len(documents) == 0 {
		return
	}
	document := documents[0]

	attempt, claimed, err := w.store.ClaimAndReturnAttempt(ctx, document.ID)
	if err != nil {
		logger.Warn("抢占任务失败，跳过这一条",
			zap.Uint64("document_id", document.ID), zap.Error(err))
		return
	}
	if !claimed {
		// 被另一个 runner 或实例抢先了，本轮没有活干。
		return
	}

	// 一个任务的 panic 不能带走整个进程：Go 里任何 goroutine 的未捕获 panic
	// 都会终止程序。这里兜住并放弃本次处理 —— 心跳会随之停止，
	// 该行几分钟内会被周期 ResetStale 回收重新排队。
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Error("收录任务 panic，已放弃本次处理",
				zap.Uint64("document_id", document.ID),
				zap.Int32("ingest_attempt", attempt),
				zap.Any("panic", recovered),
				zap.Stack("stack"),
			)
		}
	}()
	w.processOne(ctx, document, attempt)
}

// processOne 处理一条已经抢到手的任务，attempt 是这次处理的租约编号。
//
// 开始处理前先按现实材料算恢复点（resolveRecoveryStage）：切片还在就直接重新向量化，
// 切片没了但有正文就重新分块，正文也没了才重新解析原文件，三者都不在才判"请重新上传"。
// 这样既不会死守一个已经不成立的阶段，也不会在材料缺失时白白重做最贵的那一步。
//
// 阶段决定它需要什么输入：parse 必须有一份可读的原件，chunk / embed 的输入在库里，
// 原文件已经不是必需品 —— 所以路径校验只在 parse 阶段强制（见下）。校验本身仍然必要：
// 路径从 metadata 里读出来，而 metadata 的写入者对路径没有任何约束力，
// 不加这道判断就等于允许"构造一条记录、让后台进程删掉任意目录"。这道分支
// **不清理任何目录** —— 路径本身就不可信，filepath.Dir 指到哪儿都有可能。
//
// 成败对暂存文件的处置：
//   - 成功：内容已经进库，原件没有用了，删掉整个暂存目录；
//   - 失败且失败现场已落库：原件**留下来**并归档到 failed/<文档ID>/ —— 它是 parse
//     阶段重试的输入，删掉就等于把"重试这一份"的能力一起删了。归档本身还受租约
//     保护：如果用户已经点了重试、新一轮接手，旧 Worker 彻底停手（见 archiveStagedFile）；
//   - 失败但失败现场没落库（旧租约迟到、行已被删、写库本身失败）：文件一个字节都不动。
//     旧租约归档会把新一轮正在用的输入挪走；写库失败时留在原地，下一轮还能用。
//     只有"文档确实已经不在了"这一种情况才清理暂存目录；
//   - 取消（服务关停）：既不归档也不落终态，原件留在暂存目录不动 ——
//     这一行很快会被周期 ResetStale 打回 pending，下次处理还要原样用它。
func (w *Worker) processOne(ctx context.Context, document entity.KnowledgeDocument, attempt int32) {
	path := uploadPath(document.Metadata)

	// 处理之前按现实材料再算一次恢复点，而不是照抄行上的 ingest_stage：
	// 用户点重试之后材料又少了（人工动库、操作失误）时，这里会自己退回还能走的那一步。
	stage, err := w.resolveRecoveryStage(ctx, &document, path)
	if err != nil {
		if errors.Is(err, ErrRecoveryInputMissing) {
			// 原件、正文、切片全都不在了：只能失败并请用户重新上传。
			_, _ = w.ingester.failIngest(ctx, &document, attempt, "worker", ErrRecoveryInputMissing)
			return
		}
		// 算不出恢复点（DB 抖动）不是文档的错：不写终态，留给周期回收重试。
		logger.Warn("计算收录恢复点失败，本轮放弃，等待周期回收",
			zap.Uint64("document_id", document.ID), zap.Error(err))
		return
	}

	// 只有真的要读原文件（parse）时才强制校验路径：chunk / embed 的输入在库里，
	// 原件已经不在了也不该挡下它们。校验本身仍然必要 —— 路径从 metadata 里读出来，
	// 而 metadata 的写入者对路径没有任何约束力，不加这道判断就等于允许
	// "构造一条记录、让后台进程删掉任意目录"。这道分支**不清理任何目录** ——
	// 路径本身就不可信，filepath.Dir 指到哪儿都有可能。
	if stage == entity.KnowledgeDocumentStageParse && (path == "" || !w.isUnderRoot(path)) {
		payload, _ := json.Marshal(map[string]any{"error": unusablePathReason, "stage": "worker"})
		applied, err := w.store.MarkFailed(ctx, document.ID, attempt, payload, unusablePathReason)
		if err != nil {
			logger.Error("标记暂存路径无效时出错，文档可能停在中间状态",
				zap.Uint64("document_id", document.ID), zap.Error(err))
		} else if !applied {
			logger.Warn("暂存路径无效的失败现场未写入：文档已不在处理中或租约已失效",
				zap.Uint64("document_id", document.ID), zap.Int32("ingest_attempt", attempt))
		}
		return
	}

	stopHeartbeat := w.startHeartbeat(ctx, document.ID, attempt)
	defer stopHeartbeat() // 必须用 defer：任务 panic 时也要把心跳停掉，否则这一行永远不会被回收

	_, err = w.ingester.processExistingFile(ctx, &document, FileInput{
		Path:      path,
		Title:     taskTitle(document.Metadata, document.Title),
		SourceURI: sourceURI(document),
	}, attempt, stage)

	if err != nil {
		if errors.Is(err, context.Canceled) {
			logger.Info("收录任务被取消，暂存文件保留等待周期回收",
				zap.Uint64("document_id", document.ID),
				zap.String("path", path),
			)
			return
		}
		// 界面上只显示一句中文摘要（failIngest 写进 metadata 的 error 键），
		// 完整诊断（错误码 + stderr 原文）躺在 metadata 的 error_detail 里。
		// 这里再留一条日志：排障时不该为了看一句 traceback 去翻某一行文档的 JSON。
		logger.Warn("文件收录失败",
			zap.Uint64("document_id", document.ID),
			zap.Int32("ingest_attempt", attempt),
			zap.String("path", path),
			zap.Error(err),
		)

		if failureRecorded(err) {
			w.archiveStagedFile(ctx, document.ID, attempt, path)
			return
		}

		// 失败现场没写进库里：这一行可能已被删除，也可能已经属于新一轮任务。
		// 只有确认文档真的没了才清理暂存目录；其余情况一律不动文件。
		if !w.ingester.documentExists(document.ID) {
			logger.Info("文档已被删除，不再归档失败原件，直接清理暂存目录",
				zap.Uint64("document_id", document.ID))
			w.discardStagedFile(path)
		}
		return
	}
	w.discardStagedFile(path)
}

// resolveRecoveryStage 按现实材料算这次从哪一步开始（见 ResolveRecoveryStage）。
//
// 它不信任行上的 ingest_stage：阶段只是记录，材料才是事实。DB 出错时返回错误
// （调用方不该把一次查询失败记成文档失败，留给周期回收重试）；材料全无时返回
// ErrRecoveryInputMissing，由调用方写成终态并把"重新上传"告诉用户。
//
// 原件的判断只看"路径存在且文件还在"；路径是否落在上传根目录内由 processOne 在
// 确定要走 parse 之后再核对（那是防越界删除的安全边界，不是恢复点计算的一部分）。
func (w *Worker) resolveRecoveryStage(ctx context.Context, document *entity.KnowledgeDocument, path string) (string, error) {
	counts, err := w.store.CountChunksByDocument(ctx, []uint64{document.ID})
	if err != nil {
		return "", fmt.Errorf("统计已落库的切片失败: %w", err)
	}

	hasOriginal := false
	if strings.TrimSpace(path) != "" {
		if _, err := os.Stat(path); err == nil {
			hasOriginal = true
		}
	}

	return ResolveRecoveryStage(RecoveryMaterial{
		HasOriginal: hasOriginal,
		HasContent:  strings.TrimSpace(document.Content) != "",
		HasChunks:   counts[document.ID] > 0,
	})
}

// startHeartbeat 在任务存续期间持续推 updated_at，返回一个停止函数。
//
// 它的存在让周期 ResetStale 的阈值可以从"必须大于最慢的任务"降到"心跳间隔的两三倍"：
// 进程被 kill、goroutine 消失时心跳立即停，几分钟后行就被回收 ——
// 不需要等下一次进程重启，也不需要把阈值调到几十分钟。
//
// 心跳带上租约编号：旧执行者若在任务被回收、重新认领之后才醒过来，它的心跳
// 影响 0 行 —— 否则一个已经死掉的新任务会被旧心跳一直"续命"，永远等不到回收。
//
// ⚠️ 契约：今后任何新增的长阶段都必须跑在这个 ctx 下（或同样有报活），
// 否则会被 ResetStale 当成僵尸误杀。
func (w *Worker) startHeartbeat(ctx context.Context, id uint64, attempt int32) func() {
	ticker := time.NewTicker(w.heartbeatEvery)
	done := make(chan struct{})
	var once sync.Once

	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				// 关停时 ctx 已取消，心跳失败是预期内的，不必记 Warn
				if err := w.store.Touch(ctx, id, attempt); err != nil && ctx.Err() == nil {
					logger.Warn("收录心跳失败", zap.Uint64("document_id", id), zap.Error(err))
				}
			}
		}
	}()

	return func() {
		once.Do(func() {
			ticker.Stop()
			close(done)
		})
	}
}

// archiveStagedFile 把失败的原件从暂存目录挪到 failed/<文档ID>/，再把新位置写回 metadata。
//
// 全程受租约保护，分成三道：
//  1. 动文件之前先核对"这一行仍由这次失败持有"（FailedLeaseOwned）。用户可能在
//     失败现场写下的下一秒就点了重试、新一轮已经接手并在读原文件 —— 核对不过就
//     彻底停手，连指针都不碰。
//  2. 挪完之后写指针也带同样的条件（SetUploadPath）。万一在"核对通过 → 挪文件"
//     这微秒级窗口里租约被抢走，写入影响 0 行，我们就把文件挪回原位，
//     让新一轮按 metadata 里的旧位置仍能找到它。
//  3. 归档失败不阻断流程：原件留在暂存目录、upload_path 也不改 —— 它仍然在 uploadRoot
//     之下，所以重试与删除时的清理照样找得到它（见 isUnderRoot 与 service.isUploadPath）。
//     代价是它不会再被自动清理，所以这里必须留日志：那是"文件为什么残留"唯一的线索。
func (w *Worker) archiveStagedFile(ctx context.Context, documentID uint64, attempt int32, path string) {
	if strings.TrimSpace(path) == "" {
		// chunk / embed 阶段的重试不需要原文件，路径可能是空的（甚至整个文件已丢）。
		// 没有东西要归档，安静返回。
		return
	}

	owned, err := w.store.FailedLeaseOwned(ctx, documentID, attempt)
	if err != nil {
		logger.Warn("核对失败租约出错，放弃归档原件",
			zap.Uint64("document_id", documentID), zap.Int32("ingest_attempt", attempt), zap.Error(err))
		return
	}
	if !owned {
		logger.Info("文档已被重试或接手，放弃归档原件",
			zap.Uint64("document_id", documentID), zap.Int32("ingest_attempt", attempt))
		return
	}

	source := filepath.Dir(path)
	if _, err := os.Stat(source); err != nil {
		// 原件已经不在了（metadata 里的路径可能早被人工动过），没有东西要归档。
		return
	}
	if !w.isStagingDir(source) {
		// 路径来自 metadata，写入者不受约束；形态不对就不碰，免得把删除动作带到别处。
		logger.Warn("失败原件不在可归档的暂存目录内，跳过归档", zap.String("dir", source))
		return
	}

	target := filepath.Join(w.uploadRoot, failedDirName, strconv.FormatUint(documentID, 10))
	// 重试之后又失败时会第二次走到这里，而这一回原件已经躺在归档目录里了
	// （source 与 target 是同一个目录）：不用挪，也不用改指针。
	//
	// 这一步不能省。下面的"先清掉目标再挪"是给"目标里放着上一次那份"准备的，
	// 而在这里 target 就是 source —— RemoveAll 会把唯一那份原件连目录一起删掉，
	// 归档随之失败，重试的输入就此永久消失。
	if sameDirectory(source, target) {
		return
	}

	// 归档根目录要自己建：os.Rename 只挪条目，不会替调用方创建父目录，
	// 而 uploadRoot 下本来只有 pending/ —— 少了这一步，第一次归档必定失败。
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		logger.Warn("创建失败归档目录失败，原件留在暂存目录",
			zap.Uint64("document_id", documentID), zap.String("dir", target), zap.Error(err))
		return
	}
	// 重试又失败时会第二次走到这里，而目标目录里还躺着上一次那一份。
	// 先清掉：os.Rename 在目标已存在时（Windows 上尤其）会直接失败。
	if err := os.RemoveAll(target); err != nil {
		logger.Warn("清理上一次归档的失败原件失败",
			zap.Uint64("document_id", documentID), zap.String("dir", target), zap.Error(err))
	}

	if err := os.Rename(source, target); err != nil {
		logger.Warn("保留失败原件失败，原件留在暂存目录且不会被自动清理",
			zap.Uint64("document_id", documentID), zap.String("from", source), zap.Error(err))
		return
	}

	archived := filepath.Join(target, filepath.Base(path))
	applied, err := w.store.SetUploadPath(ctx, documentID, attempt, archived)
	if err != nil {
		// 文件挪走了、库里还指着旧位置 —— 这是必须让人看见的坏状态：
		// 重试会报"原件已不在"，删除时的清理也会漏掉这一份。
		logger.Error("失败原件已归档但 upload_path 未更新，重试会找不到它",
			zap.Uint64("document_id", documentID), zap.String("path", archived), zap.Error(err))
		return
	}
	if applied {
		return
	}

	// 归档与"用户点重试"挤进了同一个瞬间：指针没写进去，新一轮会按 metadata 里的
	// 旧位置找文件。把文件挪回原位，让它仍然找得到（窗口只有微秒级，挪回去时
	// 新一轮还没来得及开始读）。
	if err := os.Rename(target, source); err != nil {
		logger.Error("租约已失效且原件挪不回暂存目录，下一次重试会找不到它",
			zap.Uint64("document_id", documentID),
			zap.String("from", target), zap.String("to", source), zap.Error(err))
		return
	}
	logger.Info("归档期间租约失效，原件已挪回暂存目录",
		zap.Uint64("document_id", documentID), zap.Int32("ingest_attempt", attempt))
}

// discardStagedFile 删掉收录成功后不再需要的暂存目录。
//
// 不再用挂在 processOne 上的 defer：那个写法会让失败路径也走到删除，
// 而失败恰恰是最需要把文件留下的那条路。返回值也不再丢弃 ——
// 删不掉是有信息的（Windows 上常见于文件仍被解析器进程占用），
// 而这个目录此后没有任何人会再来清理它。
//
// 删之前先收窄范围：只允许删 <root>/pending/<一层> 或 <root>/failed/<一层>。
// path 来自 metadata、写入者不受约束；如果它直接落在 root 下，
// filepath.Dir 就是 root 本身，RemoveAll 会把整棵上传目录（含别的待处理原件）清空。
func (w *Worker) discardStagedFile(path string) {
	if strings.TrimSpace(path) == "" {
		// 没有路径可清理（chunk / embed 阶段的重试可能不再持有原文件）。
		return
	}

	directory := filepath.Dir(path)
	if !w.isStagingDir(directory) {
		logger.Warn("暂存目录不在可清理范围内，跳过删除", zap.String("dir", directory))
		return
	}
	if err := os.RemoveAll(directory); err != nil {
		logger.Warn("清理上传暂存目录失败", zap.String("dir", directory), zap.Error(err))
	}
}

// isStagingDir 判断一个目录是否是我们管理的暂存目录（上传根目录下的两层之一，
// 即 pending/<一层> 或 failed/<一层>）。
//
// 判据只要求"恰好比根目录深两层"：这样删除的影响范围永远限定在某个暂存子目录里，
// 不可能落到根目录本身。不做目录名白名单 —— 上传根目录整个归本服务管，
// 多一份白名单只会多一个会漂移的口径。
func (w *Worker) isStagingDir(dir string) bool {
	root, err := filepath.Abs(w.uploadRoot)
	if err != nil {
		return false
	}
	target, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return len(strings.Split(rel, string(os.PathSeparator))) == 2
}

// uploadPath 从 metadata 里读上传时记下的暂存文件路径，读不到返回空串。
func uploadPath(raw json.RawMessage) string {
	var metadata map[string]any
	if json.Unmarshal(raw, &metadata) != nil {
		return ""
	}
	path, _ := metadata["upload_path"].(string)
	return strings.TrimSpace(path)
}

// taskTitle 决定这条任务该用什么标题。
//
// "要不要用正文一级标题替换文件名"这个决策在提交时就定下了：上传时调用方
// 没给标题的话，SubmitFile 会记 explicit_title = false，这里就必须返回空串，
// 好让 processExistingFile 走到标题回落那一步。
// 若这里直接把 document.Title 传下去，回落会因为标题恒非空而永远不会生效。
func taskTitle(raw json.RawMessage, fallback string) string {
	var metadata map[string]any
	if json.Unmarshal(raw, &metadata) == nil {
		if explicit, ok := metadata["explicit_title"].(bool); ok && !explicit {
			return ""
		}
	}
	return fallback
}

// sourceURI 取文档的来源标识，NULL 按空串处理。
func sourceURI(document entity.KnowledgeDocument) string {
	if document.SourceURI == nil {
		return ""
	}
	return *document.SourceURI
}

// sameDirectory 判断两个目录是不是同一个。
//
// 都取绝对路径再比：source 来自 metadata（可能是相对路径），target 是这里拼出来的，
// 直接比字符串会在 "a/b" 与 "a/./b" 这种等价写法上判错。
func sameDirectory(left, right string) bool {
	leftAbs, err := filepath.Abs(left)
	if err != nil {
		return false
	}
	rightAbs, err := filepath.Abs(right)
	if err != nil {
		return false
	}
	return leftAbs == rightAbs
}

// isUnderRoot 判断路径是否落在上传根目录之内。
//
// 先取绝对路径再比相对路径，而不是做字符串前缀比较：后者会被 ../ 绕过，
// 也会把 "uploads2" 这种同前缀的兄弟目录算进来。rel 的结果等于 ".."
// 或以 "../" 开头，都说明目标在根目录之外。
func (w *Worker) isUnderRoot(path string) bool {
	root, err := filepath.Abs(w.uploadRoot)
	if err != nil {
		return false
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
