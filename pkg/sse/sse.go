// Package sse 提供 Server-Sent Events 响应的最小写入工具。
//
// 只覆盖协议里用得上的那一小部分：声明流、写事件、写注释心跳。事件 id
// （断线续传用的 Last-Event-ID）等能力等真的需要时再加 —— 目前这条流是
// "状态快照流"：重连时服务端直接补一帧当前状态，不依赖事件回放。
package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Start 把这次响应切进 SSE 模式：宣告响应头、提交 200，并解除服务器的写超时。
//
// 解除写超时是长连接的硬需求：http.Server 的 WriteTimeout 是连接级的绝对死线，
// 心跳与事件都不会重置它 —— 不解除的话，一条正常的推送流会在 60 秒时被服务端掐断。
// gin 的 ResponseWriter 实现了 Unwrap，ResponseController 才能穿透到 net/http 的
// 连接上改 deadline；测试里换成 httptest.Recorder 时这一步返回 ErrNotSupported，
// 忽略即可（那里没有超时这回事）。
func Start(c *gin.Context) {
	header := c.Writer.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	// 反代（nginx 等）默认会把响应缓冲起来再发，SSE 会被攒成一段之后才到前端。
	header.Set("X-Accel-Buffering", "no")

	_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})

	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Flush()
}

// Event 写一个事件，data 先按 JSON 序列化。
func Event(c *gin.Context, event string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return EventJSON(c, event, payload)
}

// EventJSON 写一个 data 已经是 JSON 的事件（不带 id）。
//
// 与 Event 分开是为了让调用方能"一次序列化、两处用"：进度流要拿序列化后的字节与
// 上一帧比对，只有变了才推，比对与写线用的是同一份字节。
func EventJSON(c *gin.Context, event string, payload []byte) error {
	return write(c, "", event, payload)
}

// EventWithID 写一个带 id 的事件。
//
// id 是断线续传的起点：浏览器原生 EventSource 重连时会把它放进 Last-Event-ID 头，
// fetch 版客户端（api/client.ts 的 streamEvents）自己记下来、重连时带上。
// 对话事件流用 sequence_no 当 id —— 它本来就是事件在对话里的单调序号。
func EventWithID(c *gin.Context, id int64, event string, payload []byte) error {
	return write(c, strconv.FormatInt(id, 10), event, payload)
}

// write 组装并写出一帧。data 是 JSON（没有裸换行），一行就够；
// 事件名与 id 由调用方保证不含换行。
func write(c *gin.Context, id, event string, payload []byte) error {
	var frame strings.Builder
	if id != "" {
		frame.WriteString("id: ")
		frame.WriteString(id)
		frame.WriteByte('\n')
	}
	frame.WriteString("event: ")
	frame.WriteString(event)
	frame.WriteString("\ndata: ")
	frame.Write(payload)
	frame.WriteString("\n\n")

	if _, err := fmt.Fprint(c.Writer, frame.String()); err != nil {
		return err
	}
	c.Writer.Flush()
	return nil
}

// Heartbeat 写一行注释心跳。SSE 协议里以 ':' 开头的行是注释，客户端会忽略。
//
// 两个作用：让反代与浏览器别把空闲连接掐掉；让服务端有机会发现对端已经断开
// （写失败就是断开）。
func Heartbeat(c *gin.Context) error {
	if _, err := fmt.Fprint(c.Writer, ": ping\n\n"); err != nil {
		return err
	}
	c.Writer.Flush()
	return nil
}
