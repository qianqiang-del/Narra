package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const maxAgentToolResultBytes = 32 * 1024

type toolResultCompactPolicy struct {
	maxItems       int
	maxStringRunes int
}

var (
	htmlNoisePattern = regexp.MustCompile(`(?is)<(?:script|style)\b[^>]*>.*?</(?:script|style)>`)
	htmlTagPattern   = regexp.MustCompile(`(?s)<[^>]+>`)
)

func compactToolResult(encoded []byte) ([]byte, bool, error) {
	if len(encoded) <= maxAgentToolResultBytes {
		return encoded, false, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false, fmt.Errorf("解析工具结果: %w", err)
	}

	policies := []toolResultCompactPolicy{
		{maxItems: 8, maxStringRunes: 2000},
		{maxItems: 6, maxStringRunes: 1000},
		{maxItems: 5, maxStringRunes: 500},
		{maxItems: 3, maxStringRunes: 250},
	}
	for _, policy := range policies {
		result := map[string]any{
			"_compression": map[string]any{
				"compressed":     true,
				"original_bytes": len(encoded),
				"note":           "结果已做确定性裁剪；保留来源与核心文本，Research Agent 应仅据此提取证据",
			},
			"result": compactToolValue(value, policy),
		}
		compacted, err := json.Marshal(result)
		if err != nil {
			return nil, false, fmt.Errorf("编码压缩结果: %w", err)
		}
		if len(compacted) <= maxAgentToolResultBytes {
			return compacted, true, nil
		}
	}

	fallback, err := json.Marshal(map[string]any{
		"_compression": map[string]any{
			"compressed":     true,
			"original_bytes": len(encoded),
			"note":           "原始结果过大，结构化裁剪后仍超过上下文预算；请缩小查询范围后重试",
		},
	})
	if err != nil {
		return nil, false, err
	}
	return fallback, true, nil
}

func compactToolValue(value any, policy toolResultCompactPolicy) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for childKey, child := range typed {
			if dropToolResultField(childKey, child) {
				continue
			}
			result[childKey] = compactToolValue(child, policy)
		}
		return result
	case []any:
		limit := len(typed)
		if limit > policy.maxItems {
			limit = policy.maxItems
		}
		result := make([]any, 0, limit)
		seenURLs := make(map[string]struct{}, limit)
		for _, item := range typed {
			if url := toolResultURL(item); url != "" {
				if _, exists := seenURLs[url]; exists {
					continue
				}
				seenURLs[url] = struct{}{}
			}
			result = append(result, compactToolValue(item, policy))
			if len(result) >= limit {
				break
			}
		}
		return result
	case string:
		if nested, ok := decodeNestedToolJSON(typed); ok {
			return compactToolValue(nested, policy)
		}
		return truncateToolRunes(compactToolText(typed), policy.maxStringRunes)
	default:
		return value
	}
}

func decodeNestedToolJSON(value string) (any, bool) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) < 2 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return nil, false
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, false
	}
	return decoded, true
}

func dropToolResultField(key string, value any) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case "blob", "base64", "image_data", "screenshot", "raw_html", "rawhtml":
		return true
	case "data":
		text, ok := value.(string)
		return ok && len(text) > 4096
	default:
		return false
	}
}

func compactToolText(value string) string {
	text := strings.TrimSpace(value)
	lower := strings.ToLower(text)
	if strings.Contains(lower, "<!doctype html") || strings.Contains(lower, "<html") || strings.Contains(lower, "<body") {
		text = htmlNoisePattern.ReplaceAllString(text, " ")
		text = htmlTagPattern.ReplaceAllString(text, " ")
	}
	return strings.Join(strings.Fields(text), " ")
}

func truncateToolRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "…"
}

func toolResultURL(value any) string {
	item, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"url", "uri", "link"} {
		if raw, ok := item[key].(string); ok {
			return strings.TrimSpace(raw)
		}
	}
	return ""
}
