package classroom

import (
	"context"
	"errors"
	"fmt"
)

// defaultPageConcurrency 是页面并发生成数的缺省值。
const defaultPageConcurrency = 3

// defaultTTSPoolSize 是语音合成并发上限的缺省值。
const defaultTTSPoolSize = 3

// errPageUnreachable 表示这一页的先修页始终没有结束，调度放弃它。
var errPageUnreachable = errors.New("先修页未结束，调度放弃本页")

// pageOutcome 是一页的执行结果。
type pageOutcome struct {
	task pageTask
	err  error
}

// effectivePageConcurrency 取页面并发生成数，未配置时用缺省值。
func effectivePageConcurrency(deps Deps) int {
	if deps.PageConcurrency > 0 {
		return deps.PageConcurrency
	}
	return defaultPageConcurrency
}

// effectiveTTSPoolSize 取语音合成并发上限，未配置时用缺省值。
func effectiveTTSPoolSize(deps Deps) int {
	if deps.TTSPoolSize > 0 {
		return deps.TTSPoolSize
	}
	return defaultTTSPoolSize
}

// runPages 按 prerequisites 依赖与并发上限调度页面生成，所有页结束后返回。
//
// 单页失败只记在它自己的结果里，不影响别的页；失败页同样算「已结束」，
// 依赖它的页照常执行——页与页共享的是计划，不是上游页的产物。
// 每一轮都从 tasks 头部开始找可启动的页，所以页序在前的先进入并发池。
func runPages(ctx context.Context, tasks []pageTask, concurrency int, generate func(context.Context, pageTask) error) []pageOutcome {
	if concurrency < 1 {
		concurrency = 1
	}
	results := make(chan pageOutcome, len(tasks))
	running := make(map[string]bool, len(tasks))
	finished := make(map[string]bool, len(tasks))
	outcomes := make([]pageOutcome, 0, len(tasks))

	for len(outcomes) < len(tasks) {
		for _, task := range tasks {
			if len(running) >= concurrency {
				break
			}
			planID := task.Page.PlanID
			if running[planID] || finished[planID] || !prerequisitesMet(task.Page, finished) {
				continue
			}
			running[planID] = true
			go func(item pageTask) {
				results <- pageOutcome{task: item, err: runPageSafely(ctx, generate, item)}
			}(task)
		}
		if len(running) == 0 {
			// 剩下的页全卡在依赖上。计划校验已排除成环，这里只做兜底。
			for _, task := range tasks {
				if planID := task.Page.PlanID; !finished[planID] {
					finished[planID] = true
					outcomes = append(outcomes, pageOutcome{task: task, err: errPageUnreachable})
				}
			}
			break
		}
		outcome := <-results
		delete(running, outcome.task.Page.PlanID)
		finished[outcome.task.Page.PlanID] = true
		outcomes = append(outcomes, outcome)
	}
	return outcomes
}

// runPageSafely 跑一页并把 panic 收敛成错误：页面跑在各自的 goroutine 里，
// 一次越界或空指针不该把整堂课连同服务进程一起带下去。
func runPageSafely(ctx context.Context, generate func(context.Context, pageTask) error, task pageTask) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("页面生成 panic: %v", r)
		}
	}()
	return generate(ctx, task)
}

// prerequisitesMet 判断一页的先修页是否都已结束。
func prerequisitesMet(page PlanPage, finished map[string]bool) bool {
	for _, dependency := range page.Prerequisites {
		if !finished[dependency] {
			return false
		}
	}
	return true
}

// ttsLimiter 是语音合成的并发闸门；为 nil 表示不限流。
type ttsLimiter chan struct{}

// newTTSLimiter 建闸门，size 不大于 0 时返回 nil。
func newTTSLimiter(size int) ttsLimiter {
	if size <= 0 {
		return nil
	}
	return make(ttsLimiter, size)
}

// acquire 占一个名额，ctx 取消时放弃等待。
func (l ttsLimiter) acquire(ctx context.Context) error {
	if l == nil {
		return nil
	}
	select {
	case l <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// release 归还一个名额。
func (l ttsLimiter) release() {
	if l == nil {
		return
	}
	<-l
}
