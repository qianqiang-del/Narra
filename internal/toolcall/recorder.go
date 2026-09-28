// Package toolcall 承载一次生成里工具调用失败原因的记录。
//
// 工具以 Eino 的 tool.BaseTool 交到 Agent 手里，拿到的工具可能来自远端 MCP，调用方看不出
// 它内部发生了什么。工具失败时既不能让整个 Agent 停摆，也不能让"为什么没查到"只剩一句无从
// 追查的日志，所以在 ctx 上挂一个记录器：工具层写失败原因，调用方读出来落进页面记录。
package toolcall

import (
	"context"
	"sync"
)

// Recorder 记录一次生成里工具调用失败的原因，可并发使用。
type Recorder struct {
	mutex    sync.Mutex
	failures []string
}

// NewRecorder 建一个空的工具调用记录器。
func NewRecorder() *Recorder { return &Recorder{} }

// Fail 记一条失败原因，重复的原因只留第一条。
func (r *Recorder) Fail(err error) {
	if r == nil || err == nil {
		return
	}
	text := err.Error()
	r.mutex.Lock()
	defer r.mutex.Unlock()
	for _, item := range r.failures {
		if item == text {
			return
		}
	}
	r.failures = append(r.failures, text)
}

// Cause 返回第一条失败原因，没有失败时是空串。
func (r *Recorder) Cause() string {
	if r == nil {
		return ""
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if len(r.failures) == 0 {
		return ""
	}
	return r.failures[0]
}

// recorderKey 是记录器在 ctx 上的键，不导出以免和其他包的同名键相撞。
type recorderKey struct{}

// WithRecorder 把记录器挂到 ctx 上供工具层写入。
func WithRecorder(ctx context.Context, recorder *Recorder) context.Context {
	if recorder == nil {
		return ctx
	}
	return context.WithValue(ctx, recorderKey{}, recorder)
}

// FromContext 取 ctx 上的记录器，没有则返回 nil；返回的 nil 记录器各方法可安全调用。
func FromContext(ctx context.Context) *Recorder {
	recorder, _ := ctx.Value(recorderKey{}).(*Recorder)
	return recorder
}
