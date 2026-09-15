package service

import (
	"context"
	"fmt"
	"sync"

	dto "narra/internal/model/dto/response"
	"narra/pkg/errors"
	"narra/pkg/tts"
)

// VoiceGenderFemale、VoiceGenderMale 是音色的性别取值。
const (
	VoiceGenderFemale = "女"
	VoiceGenderMale   = "男"
)

// previewText 是试听时合成的那句话。它固定不变，所以合成结果可以一直缓存。
const previewText = "同学们好，我们开始上课。"

// voices 是全部可选音色，男女各 3。
//
// 取自 Qwen 的音色列表（zh-CN），**没有试听过**，是按展示名挑的，不合适直接换 ID。
// 增删记得同步 migrations/0002_seed_preset_agents.sql 里 preset_agents.voice_id。
var voices = []dto.VoiceItem{
	{ID: "Cherry", Name: "芊悦", Gender: VoiceGenderFemale},
	{ID: "Serena", Name: "苏瑶", Gender: VoiceGenderFemale},
	{ID: "Chelsie", Name: "千雪", Gender: VoiceGenderFemale},
	{ID: "Nofish", Name: "不吃鱼", Gender: VoiceGenderMale},
	{ID: "Pip", Name: "顽屁小孩", Gender: VoiceGenderMale},
	{ID: "Ethan", Name: "晨煦", Gender: VoiceGenderMale},
}

// voiceService 音色业务实现。
type voiceService struct {
	// tts 为 nil 表示语音合成没启用。这时音色列表照常返回，只有试听会报错。
	tts *tts.Client

	// 试听音频，key 是音色 ID。用 RWMutex 而不是 Mutex：合成要走网络（秒级），
	// 不该让不同音色互相排队。
	mu    sync.RWMutex
	cache map[string][]byte
}

// NewVoiceService 创建音色业务服务。ttsClient 允许为 nil。
func NewVoiceService(ttsClient *tts.Client) VoiceService {
	return &voiceService{tts: ttsClient, cache: make(map[string][]byte)}
}

// List 返回全部可选音色。
func (s *voiceService) List(ctx context.Context) ([]dto.VoiceItem, error) {
	// 用 make 而不是 var：空结果要序列化成 [] 而不是 null，
	// 否则前端 .map() 会炸在一个看起来像"没有数据"的 null 上。
	items := make([]dto.VoiceItem, 0, len(voices))
	items = append(items, voices...)

	return items, nil
}

// Preview 合成一句固定的试听文案，返回音频字节。
func (s *voiceService) Preview(ctx context.Context, voiceID string) ([]byte, error) {
	if !IsValidVoiceID(voiceID) {
		return nil, errors.New(errors.CodeResourceNotFound, fmt.Sprintf("音色 %s 不存在", voiceID))
	}
	if s.tts == nil {
		return nil, errors.New(errors.CodeServiceUnavailable,
			"语音合成未启用：请在 configs/config.yaml 打开 tts.enabled 并设置环境变量 TTS_API_KEY")
	}

	s.mu.RLock()
	cached, ok := s.cache[voiceID]
	s.mu.RUnlock()
	if ok {
		return cached, nil
	}

	audio, err := s.tts.Synthesize(ctx, previewText, voiceID)
	if err != nil {
		return nil, errors.NewWithErr(errors.CodeInternalError, "语音合成失败", err)
	}

	s.mu.Lock()
	s.cache[voiceID] = audio
	s.mu.Unlock()

	return audio, nil
}

// IsValidVoiceID 判断音色 ID 是否在目录里。
func IsValidVoiceID(id string) bool {
	for _, v := range voices {
		if v.ID == id {
			return true
		}
	}
	return false
}
