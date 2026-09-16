package mcp

import "errors"

var (
	ErrUnknownServer  = errors.New("mcp server 未配置")
	ErrUnknownTool    = errors.New("mcp tool 不存在")
	ErrToolFailed     = errors.New("mcp tool 执行失败")
	ErrResultTooLarge = errors.New("mcp tool 结果过大")
)
