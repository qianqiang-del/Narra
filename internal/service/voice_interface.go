package service

import (
	"context"

	dto "narra/internal/model/dto/response"
)

// VoiceService 音色业务。
type VoiceService interface {
	// List 返回全部可选音色。
	List(ctx context.Context) ([]dto.VoiceItem, error)

	// Preview 合成一句固定的试听文案，返回音频字节。
	Preview(ctx context.Context, voiceID string) ([]byte, error)
}
