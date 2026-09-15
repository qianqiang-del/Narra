package agent

// 音色目录，是**产品常量**：既不建表也不进配置文件。
//
// 不建表：音色的权威是 TTS 服务（VoxCPM），不是我们。建表等于让数据库成了「哪些音色
// 存在」的权威，而没有任何东西会去纠正它——TTS 下线一个音色，表里还留着，用户选中、
// 课也生成了，直到合成那一步才炸。
//
// 不进配置文件：configs/ 下是**部署参数**（数据库地址、端口），每套环境不一样。
// 音色在所有环境都一样，和提示词、角色定义属于同一类东西。
//
// 还有一层理由来自角色进库：preset_agents 落库后，「voice_id 合法吗」从编译期检查
// 掉成了运行期检查。音色留在代码里，至少校验的一端是编译期就定死的；要是两端都进
// 数据库，就成了运行期数据校验运行期数据，一个都拦不住。
//
// 将来若 VoxCPM 暴露了音色列表接口，正确做法是把本文件内部改成启动时拉取 + 定时刷新，
// 对外的 Voices / IsValidVoiceID 两个签名不动，调用方不用改。

// Voice 一个可用的音色。
type Voice struct {
	ID   string // 音色 ID，送给 TTS 服务的就是它
	Name string // 展示名，如「温婉女声」
	Lang string // 语种，前端按它分组
}

// VoiceGroup 前端下拉框里的一个分组。
type VoiceGroup struct {
	Label  string
	Voices []Voice
}

// voiceGroups 字段与 frontend/src/data/voices.ts 的 VoiceGroup / VoiceItem 对齐：
// 那份前端常量由 GET /api/v1/voices 取代，形状保持不变，组件侧不用改。
var voiceGroups = []VoiceGroup{
	{
		Label: "VoxCPM",
		Voices: []Voice{
			{ID: "voxcpm-zh-female-warm", Name: "温婉女声", Lang: "中文"},
			{ID: "voxcpm-zh-female-clear", Name: "清亮女声", Lang: "中文"},
			{ID: "voxcpm-zh-female-calm", Name: "沉静女声", Lang: "中文"},
			{ID: "voxcpm-zh-male-lively", Name: "活泼男声", Lang: "中文"},
			{ID: "voxcpm-zh-male-young", Name: "少年音", Lang: "中文"},
			{ID: "voxcpm-zh-male-deep", Name: "低沉男声", Lang: "中文"},
		},
	},
}

// Voices 返回音色目录，供 GET /api/v1/voices 序列化。
//
// 返回副本，内外两层都要复制：调用方拿到的直接是响应体，给出内部切片的话，
// 改一下就污染了全局目录。
func Voices() []VoiceGroup {
	out := make([]VoiceGroup, len(voiceGroups))
	for i, g := range voiceGroups {
		out[i] = VoiceGroup{Label: g.Label, Voices: append([]Voice(nil), g.Voices...)}
	}
	return out
}

// IsValidVoiceID 判断音色 ID 是否在目录里。
//
// 两个地方要用，都必须由服务端做：用户提交的角色选择里带着 voice_id（客户端来的，
// 可能被改过），以及从 preset_agents 读出来的行（人工 INSERT 的，数据库只校验长度）。
//
// 不校验的后果不是保存失败，而是这个值一路带到 TTS 那一步才炸——那时课已经生成了。
func IsValidVoiceID(id string) bool {
	for _, g := range voiceGroups {
		for _, v := range g.Voices {
			if v.ID == id {
				return true
			}
		}
	}
	return false
}
