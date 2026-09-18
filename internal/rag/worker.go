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

type Worker struct {
	store       FileTaskStore
	ingester    *Ingester
	uploadRoot  string
	concurrency int
	stop        chan struct{}
	done        chan struct{}
	once        sync.Once
	cancel      context.CancelFunc
}

func NewWorker(store FileTaskStore, ingester *Ingester, uploadRoot string, concurrency int) *Worker {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Worker{store: store, ingester: ingester, uploadRoot: uploadRoot, concurrency: concurrency, stop: make(chan struct{}), done: make(chan struct{})}
}

func (w *Worker) Start() {
	go w.run()
}

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

func (w *Worker) processOne(ctx context.Context, document entity.KnowledgeDocument) {
	path := uploadPath(document.Metadata)
	if path == "" || !w.isUnderRoot(path) {
		payload, _ := json.Marshal(map[string]any{"error": "上传暂存文件不存在或路径无效", "stage": "worker"})
		_ = w.store.MarkFailed(ctx, document.ID, payload)
		return
	}
	defer os.RemoveAll(filepath.Dir(path))
	_, _ = w.ingester.processExistingFile(ctx, &document, FileInput{
		Path:      path,
		Title:     taskTitle(document.Metadata, document.Title),
		SourceURI: sourceURI(document),
	})
}

func uploadPath(raw json.RawMessage) string {
	var metadata map[string]any
	if json.Unmarshal(raw, &metadata) != nil {
		return ""
	}
	path, _ := metadata["upload_path"].(string)
	return strings.TrimSpace(path)
}

func taskTitle(raw json.RawMessage, fallback string) string {
	var metadata map[string]any
	if json.Unmarshal(raw, &metadata) == nil {
		if explicit, ok := metadata["explicit_title"].(bool); ok && !explicit {
			return ""
		}
	}
	return fallback
}

func sourceURI(document entity.KnowledgeDocument) string {
	if document.SourceURI == nil {
		return ""
	}
	return *document.SourceURI
}

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
