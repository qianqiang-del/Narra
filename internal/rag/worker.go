package rag

import (
	"context"
	"encoding/json"
	"errors"
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
// Claim 抢行、状态字段标记完成或失败。好处是任务与文档同生共死 ——
// 删除文档不会留下孤儿任务，重启也不会丢队列。
//
// concurrency 控制同时处理几篇；调度在单个进程内是串行的（每轮 process 内部
// 等所有任务跑完才开始下一轮）。多实例部署时靠 Claim 的乐观更新保证同一行
// 只被一个实例拿到。
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
}

// NewWorker 创建 worker。concurrency 小于 1 时按 1 处理：
// 收录同时吃 CPU 和上游额度，默认串行比默认并行安全。
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

// run 是轮询循环。
//
// 回收分两处：启动时先清一次上一个进程留下的僵尸行（崩溃、被 kill 时它们永远
// 停在 processing），之后在循环里周期再清 —— 只做启动那一次是不够的：
// 如果进程很快重启，那些刚被更新过的行还"不够旧"，会被启动检查漏掉，
// 而循环里再没有第二次机会，它们就只能等到下一次重启。
//
// 循环节奏是"处理一轮、等一秒"（第一个 tick 前先跑一轮，所以启动后能立刻接手
// 上一次遗留的任务）。空库时每秒一次的查询代价可以忽略，而上传之后最多一秒
// 就会被接手，用户感知不到延迟。
func (w *Worker) run() {
	defer close(w.done)

	_ = w.store.ResetStale(w.ctx, time.Now().Add(-w.staleAfter))

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	lastReset := time.Now()

	for {
		w.process(w.ctx)
		select {
		case <-w.stop:
			return
		case <-ticker.C:
		}

		if time.Since(lastReset) >= w.staleResetEvery {
			_ = w.store.ResetStale(w.ctx, time.Now().Add(-w.staleAfter))
			lastReset = time.Now()
		}
	}
}

// process 取一批 pending 文档并发处理，全部跑完才返回。
//
// 每个候选都要先 Claim 再处理：ListPending 只是一次查询，两个实例可能查到同一行。
// Claim 是一条带 status = 'pending' 条件的 UPDATE，谁把行改成了 processing
// 谁才算真的拿到任务（返回 false 就是被别人抢先了，直接跳过）。
func (w *Worker) process(ctx context.Context) {
	documents, err := w.store.ListPending(ctx, w.concurrency)
	if err != nil {
		logger.Warn("取待处理文档失败，本轮跳过", zap.Error(err))
		return
	}
	var group sync.WaitGroup
	for _, document := range documents {
		claimed, err := w.store.Claim(ctx, document.ID)
		if err != nil {
			logger.Warn("抢占任务失败，跳过这一条",
				zap.Uint64("document_id", document.ID), zap.Error(err))
			continue
		}
		if !claimed {
			continue
		}
		group.Add(1)
		go func(document entity.KnowledgeDocument) {
			defer group.Done()
			// 一个任务的 panic 不能带走整个进程：Go 里任何 goroutine 的未捕获 panic
			// 都会终止程序。这里兜住并放弃本次处理 —— 心跳会随之停止，
			// 该行几分钟内会被周期 ResetStale 回收重新排队。
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.Error("收录任务 panic，已放弃本次处理",
						zap.Uint64("document_id", document.ID),
						zap.Any("panic", recovered),
						zap.Stack("stack"),
					)
				}
			}()
			w.processOne(ctx, document)
		}(document)
	}
	group.Wait()
}

// processOne 处理一条已经抢到手的任务。
//
// 暂存文件不在上传根目录下时直接判失败：路径是从 metadata 里读出来的，
// 而 metadata 的写入者对路径没有任何约束力，不加这道判断就等于允许
// "构造一条记录、让后台进程删掉任意目录"。这条分支**不清理任何目录** ——
// 路径本身就不可信，filepath.Dir 指到哪儿都有可能。
//
// 成败对暂存文件的处置不同：
//   - 成功：内容已经进库，原件没有用了，删掉整个暂存目录；
//   - 失败：原件**留下来**并归档到 failed/<文档ID>/ —— 它是重试的输入，
//     删掉就等于把"重试这一份"的能力一起删了，用户只能重新上传一遍；
//   - 失败但文档已被删除：直接清掉暂存目录。没有重试对象，留着只会变成
//     两张表都查不到的孤儿文件（见 processOne 里 documentExists 那一步）；
//   - 取消（服务关停）：既不归档也不落终态，原件留在暂存目录不动 ——
//     这一行很快会被周期 ResetStale 打回 pending，下次处理还要原样用它。
func (w *Worker) processOne(ctx context.Context, document entity.KnowledgeDocument) {
	path := uploadPath(document.Metadata)
	if path == "" || !w.isUnderRoot(path) {
		payload, _ := json.Marshal(map[string]any{"error": unusablePathReason, "stage": "worker"})
		_ = w.store.MarkFailed(ctx, document.ID, payload, unusablePathReason)
		return
	}

	stopHeartbeat := w.startHeartbeat(ctx, document.ID)
	defer stopHeartbeat() // 必须用 defer：任务 panic 时也要把心跳停掉，否则这一行永远不会被回收

	_, err := w.ingester.processExistingFile(ctx, &document, FileInput{
		Path:      path,
		Title:     taskTitle(document.Metadata, document.Title),
		SourceURI: sourceURI(document),
	})

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
			zap.String("path", path),
			zap.Error(err),
		)

		// 归档之前先确认这一行还在：用户可能在处理期间把文档或上传记录删了。
		// 那时归档出来的 failed/<ID>/ 两张表都查不到、任何清理路径也够不着，
		// 只会永远留在磁盘上。文档都没了，也就没有重试对象，原件不必保留。
		if !w.ingester.documentExists(document.ID) {
			logger.Info("文档已被删除，不再归档失败原件，直接清理暂存目录",
				zap.Uint64("document_id", document.ID))
			w.discardStagedFile(path)
			return
		}
		w.archiveStagedFile(ctx, document.ID, path)
		return
	}
	w.discardStagedFile(path)
}

// startHeartbeat 在任务存续期间持续推 updated_at，返回一个停止函数。
//
// 它的存在让周期 ResetStale 的阈值可以从"必须大于最慢的任务"降到"心跳间隔的两三倍"：
// 进程被 kill、goroutine 消失时心跳立即停，几分钟后行就被回收 ——
// 不需要等下一次进程重启，也不需要把阈值调到几十分钟。
//
// ⚠️ 契约：今后任何新增的长阶段都必须跑在这个 ctx 下（或同样有报活），
// 否则会被 ResetStale 当成僵尸误杀。
func (w *Worker) startHeartbeat(ctx context.Context, id uint64) func() {
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
				if err := w.store.Touch(ctx, id); err != nil && ctx.Err() == nil {
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
// 归档失败不阻断流程：原件留在暂存目录、upload_path 也不改 —— 它仍然在 uploadRoot
// 之下，所以重试与删除时的清理照样找得到它（见 isUnderRoot 与 service.isUploadPath）。
// 代价是它不会再被自动清理，所以这里必须留日志：那是"文件为什么残留"唯一的线索。
func (w *Worker) archiveStagedFile(ctx context.Context, documentID uint64, path string) {
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
	if err := w.store.SetUploadPath(ctx, documentID, archived); err != nil {
		// 文件挪走了、库里还指着旧位置 —— 这是必须让人看见的坏状态：
		// 重试会报"原件已不在"，删除时的清理也会漏掉这一份。
		logger.Error("失败原件已归档但 upload_path 未更新，重试会找不到它",
			zap.Uint64("document_id", documentID), zap.String("path", archived), zap.Error(err))
	}
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
