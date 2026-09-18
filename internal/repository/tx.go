package repository

import (
	"context"

	"gorm.io/gorm"
)

// 本文件提供"一次业务操作横跨多个仓储、但必须同成功同失败"所需的最小机制。
//
// 需要它是因为下面这类写入天然跨表：追加一条用户消息（conversation_messages）之后，
// 要立刻建立这次编排（orchestration_runs）并把它和触发消息关联起来。中间任何一步失败，
// 留下的都是"用户说了话、但永远等不到回复"的半条链路 —— 数据不算脏，用户却卡住了。
//
// 做法是"把事务句柄放进 context"：
//   - service 调 TransactionManager.Run 开事务；
//   - Run 把事务句柄塞进派生出来的 ctx，再回调业务闭包；
//   - 闭包里的每个仓储方法都用 conn(ctx, r.db) 取句柄，于是自动落在同一个事务上。
//
// 这样仓储之间互不知道对方存在，service 也不需要拿到 *gorm.DB。

type txKey struct{}

// WithTx 把事务句柄放进 ctx。只由 TransactionManager 与仓储实现使用。
func WithTx(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// conn 返回本次调用应当使用的数据库句柄。
//
// 有事务就用事务，没有就直接用底层连接（此时这条语句自己是一个隐式事务，适合单表单语句的读）。
// 仓储的每个方法都必须经过它取句柄：漏掉一个，那个方法照样能跑通，只是会静默地跑到事务外面去，
// 而这类问题只在并发或回滚场景下才暴露。
func conn(ctx context.Context, db *gorm.DB) *gorm.DB {
	if tx, ok := ctx.Value(txKey{}).(*gorm.DB); ok {
		return tx.WithContext(ctx)
	}
	return db.WithContext(ctx)
}

// TransactionManager 把若干仓储写入包进一个数据库事务。
//
// 只有跨仓储的原子写入才需要它。单表单语句的写入不用：每个仓储方法本身已经是原子的。
type TransactionManager interface {
	Run(ctx context.Context, fn func(ctx context.Context) error) error
}

type transactionManager struct {
	db *gorm.DB
}

// NewTransactionManager 创建事务管理器。
func NewTransactionManager(db *gorm.DB) TransactionManager {
	return &transactionManager{db: db}
}

// Run 开启事务并回调 fn，fn 返回错误则整体回滚。
//
// fn 拿到的 ctx 已经携带事务句柄，把它一路往下传，参与调用的仓储就都在事务里。
// 注意 fn 内不要用 context.Background() 另起调用链 —— 那会重新落回事务外的连接，
// 变成"一部分写进了事务、一部分没进"。
func (m *transactionManager) Run(ctx context.Context, fn func(ctx context.Context) error) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(WithTx(ctx, tx))
	})
}
