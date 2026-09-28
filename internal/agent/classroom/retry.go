package classroom

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// maxTransientRetry 是一次模型调用出错后的重试次数。
const maxTransientRetry = 1

// maxTTSRetry 是一段讲稿合成失败后的重试次数；TTS 是纯网络 IO，抖动比模型调用多。
const maxTTSRetry = 2

// maxValidateRetry 是一页输出不合规后的重试次数；结构解析失败与校验失败合并计数，避免次数相乘。
const maxValidateRetry = 2

// maxRevisionRounds 是一页最多被审核打回的轮次。
// 保留一轮：审核提的问题仍然会被修一次；第二轮起收益递减，而每轮都要把下游重跑一遍。
const maxRevisionRounds = 1

// maxPageModelCalls 是一页的模型调用总次数上限，五个节点的调用都从这里扣。
const maxPageModelCalls = 10

// maxResearchStep 是调研 Agent 的图执行步数上限，容得下多次工具往返加一次交卷。
const maxResearchStep = 12

// retryBackoff 是两次重试之间的固定等待。
const retryBackoff = time.Second

// maxPageContentBytes 是一页内容的字节上限（编码后）。
//
// 深交互页会带上整份交互配置，模型偶尔把示例 HTML 也抖进来。体积失控的代价是双份的：
// 落进 jsonb 之后每次读页面都要传，前端渲染也扛不住。超限按校验失败处理，
// 把话回灌给模型让它自己收，而不是静默截断——截断会破坏 JSON 结构。
const maxPageContentBytes = 32 * 1024

// errBudgetExceeded 是整课时长预算到点的标记，作为 context 的 Cause 传下去。
//
// 用它而不是看 ctx.Err() == DeadlineExceeded：任务层（asynq）也会给 ctx 设截止时间，
// 光看错误类型分不清是预算到点还是任务超时，而这两种情况的收尾方式不同。
var errBudgetExceeded = errors.New("整课生成超过时长上限")

// errMalformedOutput 表示模型输出的内容不是约定结构，属于带反馈重试能修的一类。
type errMalformedOutput struct{ err error }

// Error 返回底层错误信息。
func (e errMalformedOutput) Error() string { return e.err.Error() }

// Unwrap 暴露底层错误供 errors.As 判断。
func (e errMalformedOutput) Unwrap() error { return e.err }

// malformedOutput 把一个解析错误包成可重试的输出错误。
func malformedOutput(err error) error { return errMalformedOutput{err: err} }

// isMalformedOutput 判断错误是否属于输出结构不合法。
func isMalformedOutput(err error) bool {
	var target errMalformedOutput
	return errors.As(err, &target)
}

// pageBudget 是一页的模型调用预算；一页内各节点串行执行，不需要加锁。
type pageBudget struct{ used int }

// trySpend 记一次模型调用并报告预算是否还够；不够时调用方停止重试与修订。
func (b *pageBudget) trySpend() bool {
	if b.used >= maxPageModelCalls {
		return false
	}
	b.used++
	return true
}

// exhausted 判断预算是否已用尽。
func (b *pageBudget) exhausted() bool { return b.used >= maxPageModelCalls }

// invokeWithRetryIf 按 retryable 判据重试最多 maxRetry 次，ctx 取消立即放弃。
func invokeWithRetryIf[T any](ctx context.Context, maxRetry int, retryable func(error) bool, invoke func() (T, error)) (T, error) {
	var zero T
	var lastErr error
	for attempt := 0; attempt <= maxRetry; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return zero, ctx.Err()
			case <-time.After(retryBackoff):
			}
		}
		result, err := invoke()
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !retryable(err) {
			break
		}
	}
	return zero, lastErr
}

// retryableModelError 判断模型调用错误是否属于可重试的临时故障。
func retryableModelError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "429") || strings.Contains(text, "500") || strings.Contains(text, "502") || strings.Contains(text, "503") || strings.Contains(text, "504") || strings.Contains(text, "timeout") || strings.Contains(text, "temporary") || strings.Contains(text, "connection")
}

// retryableTTSError 判断语音合成错误是否属于可重试的临时故障。
func retryableTTSError(err error) bool { return retryableModelError(err) }

// retryableDBError 判断数据库错误是否属于可重试的临时故障。
func retryableDBError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "connection") || strings.Contains(text, "deadlock") || strings.Contains(text, "timeout") || strings.Contains(text, "temporarily")
}

// outputFeedback 把一次失败的输出整理成给模型的修正提示。
func outputFeedback(err error) string {
	if isMalformedOutput(err) {
		return fmt.Sprintf("上一次输出不是合法 JSON：%s\n请只输出约定的 JSON，不要解释、不要代码围栏。", err.Error())
	}
	return fmt.Sprintf("上一次输出没有通过校验：%s\n请修正后重新输出完整结果。", err.Error())
}
