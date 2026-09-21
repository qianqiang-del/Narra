package entity

import "encoding/json"

// 场景类型 scenes.type（§4.4）。
// 只是场景的分类标签，不参与渲染——前端按 content.blocks 里每个 block 的 type 渲染；
// 它用于大纲排课（interactive 模式下排入更多交互演示页）、侧栏分类上色和识别课程完成页。
const (
	SceneTypeSlide       = "slide"
	SceneTypeQuiz        = "quiz"
	SceneTypeInteractive = "interactive"
	SceneTypePBL         = "pbl"
	SceneTypeComplete    = "complete" // 课程完成页
)

// 场景状态 scenes.status（§5.2）。
//
// 只有一个状态，不拆 content / narration 两个。曾经拆过，理由是「页面好了但讲解还没好」这个
// 中间态；但进门条件本来就是「文本和音频都齐」，这个中间态没有任何消费方——前端 data/scenes.ts
// 也一直只有一个 SceneStatus 字段。Ready 的含义是页面 JSON 与它全部讲解段落都已就绪。
const (
	SceneStatusPending    = "pending"    // 已有大纲，等待生成
	SceneStatusGenerating = "generating" // 正在生成页面 JSON 或讲解段落
	SceneStatusReady      = "ready"      // 页面 JSON 与全部讲解段落均已生成，可完整播放
	SceneStatusFailed     = "failed"     // 生成失败；跳过该场景、继续生成后面的，这一页不会再补齐
)

// Scene 课程场景/课件页，对应表 scenes（设计文档 §4.4）。
//
// Status 是单一状态，同时覆盖页面 JSON 与讲解段落两关：两者都就绪才算 Ready。
// 讲解段落各自的状态在 SceneSegment.Status（§4.5），本表的 Status 是它的汇总——
// 只要还有一个段落没就绪，本场景就不是 Ready。
//
// 约束（§4.4）：UNIQUE (classroom_id, sort_order) 由两个字段上同名的 uniqueIndex tag 声明；
// Type、Status、SortOrder 三条 CHECK 由字段上的 check tag 声明；外键
// scenes_classroom_id_fkey 由下方 Classroom 关联字段声明。全部由 AutoMigrate 建。
//
// 那条唯一约束用普通 UNIQUE 即可：生成流程按顺序逐页插入，插完没有任何入口会调换顺序
// （Pro 工作台整体推迟到 V2，设计文档 §9.5）。将来真做工作台时要改成 DEFERRABLE
// INITIALLY IMMEDIATE —— 重排会让 sort_order 中途出现同号，普通唯一约束在语句结束时
// 就会报冲突，重排做不下去。届时配合 SET CONSTRAINTS ... DEFERRED 把检查推到 COMMIT。
type Scene struct {
	BaseModel

	ClassroomID uint64 `gorm:"column:classroom_id;not null;uniqueIndex:scenes_classroom_id_sort_order_key;comment:所属课程 ID，指向 classrooms.id；课程删除时级联删除" json:"classroom_id"`                                                                                                 // 所属课程；级联删除
	SortOrder   int32  `gorm:"column:sort_order;not null;uniqueIndex:scenes_classroom_id_sort_order_key;check:scenes_sort_order_check,sort_order >= 0;comment:场景在课程里的顺序，从 0 开始；与 classroom_id 组成唯一约束" json:"sort_order"`                                                   // 场景顺序，从 0 开始
	Type        string `gorm:"column:type;type:varchar(32);not null;check:scenes_type_check,type IN ('slide', 'quiz', 'interactive', 'pbl', 'complete');comment:场景分类标签，不参与渲染（前端按 content 里每个 block 的 type 渲染），取值 slide / quiz / interactive / pbl / complete" json:"type"` // 场景分类标签，不参与渲染；slide | quiz | interactive | pbl | complete
	Title       string `gorm:"column:title;type:varchar(200);not null;comment:场景标题；由大模型产出，写入前截断到 200 字以内" json:"title"`                                                                                                                                                    // 场景标题；由大模型产出，写入前须按字符截断到 200 以内

	Status string `gorm:"column:status;type:varchar(32);not null;check:scenes_status_check,status IN ('pending', 'generating', 'ready', 'failed');comment:场景状态，取值 pending / generating / ready / failed；ready 表示页面 JSON 与它全部讲解段落都已就绪" json:"status"` // 场景状态，见 §5.2；Ready = 页面 JSON 与全部讲解段落都已就绪

	// Classroom 仅供 AutoMigrate 建外键 scenes_classroom_id_fkey（ON DELETE CASCADE）。
	// 业务代码禁止给它赋值或 Preload。
	Classroom *Classroom `gorm:"foreignKey:ClassroomID;constraint:scenes_classroom_id_fkey,OnDelete:CASCADE" json:"-"`

	// Content 场景内容，无论场景 Type 是什么都统一为 {"blocks":[...]}，例如：
	//   {"blocks":[{"key":"intro-variable","type":"paragraph","content":"..."}]}
	// 前端按每个 block 的 type 渲染，与场景的 Type 无关。block 的 type 取值是一份
	// 前后端契约（heading、paragraph、list-item、callout、code、quiz、browser、columns），
	// 每种 type 的 JSON schema 由后端领域层校验；同一场景内 block 的 key 不得重复，
	// 且必须能被 scene_segments.content_key 找到；大模型只生成结构化 JSON，不生成 HTML。
	//
	// block 是最小的可高亮单位：content_key 只指向具体 block，不支持 list.2 这类子路径，
	// 所以列表的每个要点、分栏里的每个条目都各占一个 block。
	Content json.RawMessage `gorm:"column:content;type:jsonb;not null;default:'{}';comment:页面内容 JSON，固定是一个 blocks 数组（形如 {“blocks”: [...]}）；前端按每个 block 的 type 渲染，block.key 是讲解段落挂钩子的地方" json:"content"`

	// ErrorMessage 本场景生成失败的错误摘要。一页失败不会中断流程——跳过它、继续生成后面的
	// 场景，所以这一页为什么没出得来只有这里记。与 classrooms.generation_error 分工不同：
	// 那边记的是「这门课为什么没做完」，这一列记的是「这一页为什么没生成出来」。
	// 只写能给人看的摘要，禁止写入原始 API 响应、堆栈和密钥，落库前按字符截断（建议 500）。
	// 类型用 text 而非 varchar：varchar 超长是报错，会把整个生成事务回滚掉。
	ErrorMessage *string `gorm:"column:error_message;type:text;comment:这一页为什么没生成出来的一句话；一页失败不中断整门课，后面的场景继续生成" json:"error_message"`
}

// TableName 返回表名。
func (Scene) TableName() string { return "scenes" }
