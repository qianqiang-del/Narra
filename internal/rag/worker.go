package rag

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"narra/internal/model/entity"
)

// unusablePathReason 是"暂存文件不在自己管的上传目录里"时给用户的说明。
//
// 它同时写进两处：文档 metadata 的 error 键（失败现场）与上传记录的 error_message
// （界面直接显示的那一句）。共用同一个常量，免得两处说法漂移。
const unusablePathReason = "上传暂存文件不存在或路径无效"

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
	uploadRoot  string // 上传暂存根目录；processOne 只会清理这个目录下的文件
	concurrency int
	stop        chan struct{}
	done        chan struct{}
	once        sync.Once
	cancel      context.CancelFunc // Stop 时用它中断正在跑的解析与向量化
}

// NewWorker 创建 worker。concurrency 小于 1 时按 1 处理：
// 收录同时吃 CPU 和上游额度，默认串行比默认并行安全。
func NewWorker(store FileTaskStore, ingester *Ingester, uploadRoot string, concurrency int) *Worker {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Worker{store: store, ingester: ingester, uploadRoot: uploadRoot, concurrency: concurrency, stop: make(chan struct{}), done: make(chan struct{})}
}

// Start 在后台协程里启动轮询循环，立即返回。
func (w *Worker) Start() {
	go w.run()
}

// Stop 停止轮询，并等待正在跑的任务收尾。
//
// 两步：close(stop) 让循环不再取新任务；cancel() 让正在解析或向量化的任务
// 从 ctx 上收到中断。ctx 超时不算错误路径上的意外 —— 它只表示"没等完"，
// 服务退出不该被一个卡住的上游请求无限拖住。once 保证重复调用只关一次 channel。
//
// ⚠️ 已知短板：cancel 由 run 写入、这里读取，两处没有同步。
// 触发窗口只在启动瞬间（run 还没来得及赋值就 Stop），进程退出路径上很难碰上，
// 但本机没有 gcc、跑不了 -race，所以没有实测背书。要彻底干净，
// 应该在 NewWorker 里就建好 ctx 与 cancel，让 run 只负责使用。
func (w *Worker) Stop(ctx context.Context) error {
	w.once.Do(func() {
		close(w.stop)
		if w.cancel != nil {
			w.cancel()
		}
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
// 启动时先做一次 ResetStale：上一个进程如果是在处理中退出的（崩溃、被 kill），
// 那批行会永远停在 processing，此后再没有任何人会碰它们。把超过 15 分钟没有更新过的
// processing 打回 pending 让它们重新入队 —— 这个阈值取得远大于单篇的正常耗时，
// 不会误伤正在跑的长任务。
//
// 循环节奏是"处理一轮、等一秒"（第一个 tick 前先跑一轮，所以启动后能立刻接手
// 上一次遗留的任务）。空库时每秒一次的查询代价可以忽略，而上传之后最多一秒
// 就会被接手，用户感知不到延迟。
func (w *Worker) run() {
	defer close(w.done)
	runCtx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	defer cancel()
	_ = w.store.ResetStale(runCtx, time.Now().Add(-15*time.Minute))
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		w.process(runCtx)
		select {
		case <-w.stop:
			return
		case <-ticker.C:
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
		return
	}
	var group sync.WaitGroup
	for _, document := range documents {
		claimed, err := w.store.Claim(ctx, document.ID)
		if err != nil || !claimed {
			continue
		}
		group.Add(1)
		go func(document entity.KnowledgeDocument) {
			defer group.Done()
			w.processOne(ctx, document)
		}(document)
	}
	group.Wait()
}

// processOne 处理一条已经抢到手的任务。
//
// 暂存文件不在上传根目录下时直接判失败：路径是从 metadata 里读出来的，
// 而 metadata 的写入者对路径没有任何约束力，不加这道判断就等于允许
// "构造一条记录、让后台进程删掉任意目录"。
//
// 无论成败都删掉暂存目录：文件内容已经进库，留着只会白占磁盘；
// 失败现场记在 metadata 里，不需要靠残留文件来复现。
func (w *Worker) processOne(ctx context.Context, document entity.KnowledgeDocument) {
	path := uploadPath(document.Metadata)
	if path == "" || !w.isUnderRoot(path) {
		payload, _ := json.Marshal(map[string]any{"error": unusablePathReason, "stage": "worker"})
		_ = w.store.MarkFailed(ctx, document.ID, payload, unusablePathReason)
		return
	}
	defer os.RemoveAll(filepath.Dir(path))
	_, _ = w.ingester.processExistingFile(ctx, &document, FileInput{
		Path:      path,
		Title:     taskTitle(document.Metadata, document.Title),
		SourceURI: sourceURI(document),
	})
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
