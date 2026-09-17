package entity

// 讲解段落状态 scene_segments.status（§4.5）。
const (
	SceneSegmentStatusPending    = "pending"
	SceneSegmentStatusGenerating = "generating"
	SceneSegmentStatusReady      = "ready"
	SceneSegmentStatusFailed     = "failed"
)

// SceneSegment 场景讲解段落，对应表 scene_segments（设计文档 §4.5）。
//
// 每条记录通过稳定的 ContentKey 对应 scenes.content.blocks 中的一个具体内容块，
// 前端再将该 key 写入 HTML 元素的 data-content-key 属性，用于高亮、滚动、逐段播放和 TTS。
// 不能只依赖数组下标——用户编辑或重新排序场景内容后下标会变化。
//
// 本表不重复保存内容块类型：类型统一由 scenes.content.blocks[].type 提供，前端通过
// ContentKey 找到对应 block 后读取，避免数据库字段与场景 JSON 类型不一致。
//
// 本表也不记录发言角色：讲解只由教师发声（§4.3），存下来就是个恒等于教师行的常量，
// 还要额外校验它属不属于本课程——而这条「属于同一课程」的约束从本表这一侧根本写不成
// 普通外键（本表只直接挂 scene_id，要经由 scenes.classroom_id 才推得出课程）。
// 所以干脆不存，发言角色就是教师，由 classroom_agents 里 agent_key = 'teacher' 那一行确定。
// 教师音色同样不在此重复保存，从 classroom_agents.voice_id 读取。
// 将来若开放多角色讲解，再加一列发言角色外键。
//
// Status 覆盖讲稿与 TTS 两关，但只有一个值，所以失败时要靠 Text 区分是哪一关挂的：
// Text 非空 + failed = 讲稿写出来了、挂的是音频合成，重试只需重做音频；Text 为空 + failed
// = 讲稿本身没生成出来，整段重做。Text 是 not null，讲稿未生成时存空串而非 NULL，
// 这条判断才成立。
//
// Status 为 ready 意味着「这一段能播了」，因此要求 AudioPath 非空（见 §4.5 的 CHECK）。
// 音频时长不落库，前端从音频元素自身读（HTMLAudioElement.duration）。
//
// 约束（§4.5）：UNIQUE (scene_id, content_key)、UNIQUE (scene_id, sort_order)。
// scene_id 同时参与这两个复合唯一约束——GORM 的 tag 解析的是原始 tag 字符串
// （schema/index.go parseFieldIndexes 按分号逐段处理），同一字段写两段同名的
// uniqueIndex 是被支持的，两条约束都由 AutoMigrate 建。
//
// UNIQUE (scene_id, sort_order) 用普通 UNIQUE 即可，理由与 scenes 那条相同（V1 不重排，
// 见 §4.4）。将来工作台落地时，两条要一起改成可延迟，讲解顺序会跟着页面重排一起挪位。
type SceneSegment struct {
	BaseModel

	SceneID uint64 `gorm:"column:scene_id;not null;uniqueIndex:scene_segments_scene_id_content_key_key;uniqueIndex:scene_segments_scene_id_sort_order_key" json:"scene_id"` // 所属场景；级联删除

	ContentKey string `gorm:"column:content_key;type:varchar(120);not null;uniqueIndex:scene_segments_scene_id_content_key_key" json:"content_key"`                                  // 对应场景 JSON 内容块的稳定 key；由大模型产出，写入前须按字符截断到 120 以内
	SortOrder  int32  `gorm:"column:sort_order;not null;uniqueIndex:scene_segments_scene_id_sort_order_key;check:scene_segments_sort_order_check,sort_order >= 0" json:"sort_order"` // 讲解播放顺序
	Text       string `gorm:"column:text;type:text;not null" json:"text"`                                                                                                            // 老师实际讲解的文本
	Status     string `gorm:"column:status;type:varchar(32);not null;check:scene_segments_status_check,status IN ('pending', 'generating', 'ready', 'failed')" json:"status"`        // pending | generating | ready | failed；ready = 讲稿与音频都就绪，可播

	// Scene 仅供 AutoMigrate 建外键 scene_segments_scene_id_fkey（ON DELETE CASCADE）。
	// 业务代码禁止给它赋值或 Preload。
	Scene *Scene `gorm:"foreignKey:SceneID;constraint:scene_segments_scene_id_fkey,OnDelete:CASCADE" json:"-"`

	// ready_has_audio 这条 CHECK 跨 status 与 audio_path 两列，挂在 AudioPath 上（每字段限一条 check tag）。
	AudioPath    *string `gorm:"column:audio_path;type:text;check:scene_segments_ready_has_audio_check,status <> 'ready' OR audio_path IS NOT NULL" json:"audio_path"` // TTS 音频文件相对路径；ready 时必须非空
	ErrorMessage *string `gorm:"column:error_message;type:text" json:"error_message"`                                                                                  // 讲稿或 TTS 失败摘要，禁止写入密钥
}

// TableName 返回表名。
func (SceneSegment) TableName() string { return "scene_segments" }
