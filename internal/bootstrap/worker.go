package bootstrap

import (
	"context"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"go.uber.org/zap"

	"narra/internal/agent/classroom"
	"narra/internal/service"
	"narra/internal/worker"
	"narra/pkg/config"
	"narra/pkg/logger"
)

// WorkerRuntime 持有生成任务的消费端与投递端，并周期对账，供应用启停。
type WorkerRuntime struct {
	Server *worker.Server
	Client *asynq.Client

	deps     classroom.Deps
	queue    service.JobQueue
	interval time.Duration

	cancel context.CancelFunc
	done   chan struct{}
}

// Start 启动生成任务消费端与周期对账。
func (r *WorkerRuntime) Start() error {
	if err := r.Server.Start(); err != nil {
		return err
	}
	r.startReconcile()
	return nil
}

// Shutdown 停止周期对账与消费端，并关闭投递端连接。
func (r *WorkerRuntime) Shutdown() {
	r.stopReconcile()
	r.Server.Shutdown()
	_ = r.Client.Close()
}

// startReconcile 起一个后台协程周期对账，间隔不大于 0 时不启动。
func (r *WorkerRuntime) startReconcile() {
	if r.interval <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.done = make(chan struct{})

	go func() {
		defer close(r.done)
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := ReconcileGenerating(ctx, r.deps, r, r.queue); err != nil {
					logger.Warn("周期对账失败", zap.Error(err))
				}
			}
		}
	}()
}

// stopReconcile 停止周期对账并等它退出。
func (r *WorkerRuntime) stopReconcile() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	<-r.done
}

// BuildWorker 装配生成任务的投递端与消费端，返回课堂服务要用的队列与运行实例。
func BuildWorker(deps classroom.Deps, cfg *config.Config) (service.JobQueue, *WorkerRuntime, error) {
	redisOpt := redisOpt(cfg)

	client := asynq.NewClient(redisOpt)
	// 生成任务队列依赖 Redis，连不上就直接失败，别带着一个投不出去的队列启动。
	if err := client.Ping(); err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("连接 Redis 失败（生成任务队列依赖它）: %w", err)
	}

	server := worker.NewServer(redisOpt, cfg.Worker.Concurrency)
	server.Register(worker.TypeClassroomGenerate, func(ctx context.Context, payload []byte) error {
		classroomID, err := worker.DecodeClassroomGenerate(payload)
		if err != nil {
			return worker.Permanent(err)
		}
		if err := classroom.Generate(ctx, deps, classroomID); err != nil {
			logger.Error("课堂生成失败",
				zap.Uint64("classroom_id", classroomID),
				zap.Int("retried", worker.Retried(ctx)),
				zap.Error(err),
			)
			if worker.Exhausted(ctx) {
				classroom.MarkFailed(ctx, deps, classroomID, err.Error())
			}
			return err
		}
		return nil
	})

	queue := worker.NewQueue(client, cfg.Worker.MaxRetry, cfg.Worker.Timeout)
	runtime := &WorkerRuntime{
		Server:   server,
		Client:   client,
		deps:     deps,
		queue:    queue,
		interval: cfg.Worker.ReconcileInterval,
	}
	return queue, runtime, nil
}

// redisOpt 返回 asynq 连接 Redis 的配置。
func redisOpt(cfg *config.Config) asynq.RedisClientOpt {
	redisCfg := cfg.Database.Redis
	return asynq.RedisClientOpt{
		Addr:     fmt.Sprintf("%s:%d", redisCfg.Host, redisCfg.Port),
		Password: redisCfg.Password,
		DB:       redisCfg.DB,
	}
}

// ReconcileGenerating 对账：队列里已不会继续处理的 generating 课程，归档的判失败、丢了的重投。
func ReconcileGenerating(ctx context.Context, deps classroom.Deps, runtime *WorkerRuntime, queue service.JobQueue) error {
	ids, err := deps.Classrooms.ListGeneratingIDs(ctx)
	if err != nil {
		return fmt.Errorf("读取生成中的课程失败: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}

	// 归档 = 重试已耗尽、不会再自动跑，对应课程要判失败。
	archived, err := runtime.Server.ArchivedTaskIDs()
	if err != nil {
		logger.Warn("读取归档任务失败，跳过对账", zap.Error(err))
		return nil
	}
	for _, id := range ids {
		taskID := worker.ClassroomTaskID(id)
		if _, ok := archived[taskID]; ok {
			logger.Warn("生成任务已归档，课程标记失败", zap.Uint64("classroom_id", id))
			classroom.MarkFailed(ctx, deps, id, "生成任务重试次数已耗尽")
			continue
		}
		exists, err := runtime.Server.TaskExists(taskID)
		if err != nil {
			logger.Warn("查询生成任务状态失败", zap.Uint64("classroom_id", id), zap.Error(err))
			continue
		}
		if exists {
			continue
		}
		if err := queue.Enqueue(id); err != nil {
			logger.Error("重新投递生成任务失败", zap.Uint64("classroom_id", id), zap.Error(err))
			continue
		}
		logger.Info("已重新投递生成任务", zap.Uint64("classroom_id", id))
	}
	return nil
}
