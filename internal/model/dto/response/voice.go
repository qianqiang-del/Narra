package response

type VoiceItem struct {
	ID     string `json:"id"`     // 提交角色音色时用它
	Name   string `json:"name"`   // 展示名
	Gender string `json:"gender"` // 女 / 男
}

type VoicePreview struct {
	VoiceID string `json:"voice_id"` // 回传，前端拿它确认是不是自己点的那一个
	Format  string `json:"format"`   // 目前只有 wav
	Audio   string `json:"audio"`    // base64 编码的音频
}
