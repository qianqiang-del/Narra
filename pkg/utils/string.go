package utils

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
)

// GenerateRandomString 生成随机字符串
func GenerateRandomString(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes)[:length], nil
}

// Contains 检查字符串是否在切片中
func Contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// TrimSpace 去除首尾空格
func TrimSpace(s string) string {
	return strings.TrimSpace(s)
}

// IsEmpty 检查字符串是否为空
func IsEmpty(s string) bool {
	return TrimSpace(s) == ""
}

// OptionalString 把"空串"变成 nil，其余情况返回去掉首尾空格后的值。
//
// 用于写可空列：NULL 表示"没有这一项"，空串表示"明确写成了空"，
// 两者在业务上不是一回事（例如 embedding_models.base_url 为空表示沿用全局配置）。
// 判空用去空格后的值，免得一个只有空格的输入被存成一列看起来有内容的空白。
func OptionalString(s string) *string {
	trimmed := TrimSpace(s)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
