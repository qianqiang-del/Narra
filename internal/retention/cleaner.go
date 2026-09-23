// Package retention 定期清理过期的过程数据（对话事件、追踪片段）。
//
// 这些表是"过程记录"：写入时带 expires_at，过期就该删 —— 不删的话它们会一直涨，
// 而它们记录的事实（消息、运行、回合）本来就有更长的生命周期。清理是尽力而为的
// 后台任务：一张表删失败不影响别的表，这一轮删失败下一轮再来。
package retention

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"narra/pkg/logger"
)

// 默认节奏。与 rag.Worker 同一种做法：不做成配置，测试通过字段调小它们。
const (
	// defaultInterval 两轮清理之间的间隔。过期时间以天计，一小时一轮绰绰有余。
	defaultInterval = time.Hour

	// defaultTimeout 单轮清理的超时：一次 DELETE 扫全表，卡住时不能让协程挂死。
	defaultTimeout = 30 * time.Second
)

// Expirer 是一张"按 expires_at 清理"的表的最小依赖面，由仓储实现。
type Expirer interface {
	// DeleteExpired 删除 expires_at 早于 before 的行，返回删除条数。
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}

// Table 是一张要清理的表：名字只用于日志，动作由仓储实现。
//
// 不要求仓储提供 Name()：日志口径是清理任务的事，不该渗进数据访问层。
type Table struct {
	Name  string
	Store Expirer
}

// Cleaner 周期清理过期数据。
type Cleaner struct {
	tables   []Table
	interval time.Duration
	timeout  time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// New 创建清理任务。没有任何表时也照常工作（跑空轮），调用方不必判空。
func New(tables ...Table) *Cleaner {
	ctx, cancel := context.WithCancel(context.Background())
	return &Cleaner{
		tables:   tables,
		interval: defaultInterval,
		timeout:  defaultTimeout,
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
}

// Start 在后台协程里启动清理循环，立即返回。
func (c *Cleaner) Start() {
	go c.run()
}

// Stop 停止循环并等它退出。ctx 超时表示"没等完"，不是清理失败。
func (c *Cleaner) Stop(ctx context.Context) error {
	c.once.Do(c.cancel)
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run 是清理循环。
//
// 启动时先清一次：服务可能停了几天，攒下的过期数据不该再等一个周期。
// 之后按 interval 周期跑，直到 Stop。
func (c *Cleaner) run() {
	defer close(c.done)

	c.sweep()
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.sweep()
		}
	}
}

// sweep 清一轮：每张表一次，单表失败只记日志、继续下一张。
//
// before 取"现在"：expires_at 在写入时就按各自保留期算好了（事件 7 天、追踪 30 天），
// 清理侧不需要知道保留期，只认这一列。
func (c *Cleaner) sweep() {
	ctx, cancel := context.WithTimeout(c.ctx, c.timeout)
	defer cancel()

	before := time.Now()
	for _, table := range c.tables {
		deleted, err := table.Store.DeleteExpired(ctx, before)
		if err != nil {
			// 关停时 ctx 已取消，这一轮注定失败，不必刷 Warn。
			if c.ctx.Err() == nil {
				logger.Warn("过期数据清理失败", zap.String("table", table.Name), zap.Error(err))
			}
			continue
		}
		if deleted > 0 {
			logger.Info("过期数据已清理",
				zap.String("table", table.Name),
				zap.Int64("deleted", deleted),
			)
		}
	}
}
