package entity

// 角色来源 classroom_agents.source（§4.3）。
// 教师恒为 preset；两者的完整区别见 ClassroomAgent 的类型注释。
const (
	ClassroomAgentSourcePreset = "preset" // 名称与提示词沿用代码预设（教师恒为本值）
	ClassroomAgentSourceAuto   = "auto"   // 槽位仍来自预设，但名称与提示词由大模型按课程内容重写
)

// 角色类型 classroom_agents.role_type（§4.3）。
// 跟随槽位固定，不随生成变化：teacher 只有一个，其余为 assistant 或 student。
const (
	ClassroomAgentRoleTypeTeacher   = "teacher"
	ClassroomAgentRoleTypeAssistant = "assistant"
	ClassroomAgentRoleTypeStudent   = "student"
)

// ClassroomAgent 课程角色快照，对应表 classroom_agents（设计文档 §4.3）。
//
// 本表保存一节课程最终实际使用的角色，不是全局角色库：角色的默认定义、系统提示词模板
// 和可用音色目录仍由代码/配置维护，生成时将最终使用的角色信息复制到这里，
// 保证后续回放和重试不受默认配置变化影响。
//
// 槽位固定为 6 个（教师 + 5 个预设学生），AgentKey 始终是这 6 个之一，永不为空。
// 教师恒有一行且只有一行；学生 0 到 5 行，用户一个都不勾就是 0 行。生成时哪些字段会变，
// 取决于 Source：
//
//	agent_key                        preset 为用户勾选的槽位；auto 由系统从这 6 个中选定
//	name / persona / system_prompt   preset 沿用代码预设；auto 由大模型重写（纯文本）
//	avatar / voice_id                preset 跟随槽位；auto 由大模型从那 6 套预设值中挑选
//	role / role_type / color         跟随槽位，两种来源下都由后端从代码预设取出
//
// Source 为 auto 只表示大模型重写了名称、人设与提示词，不表示它创建了新的角色定义：
// 槽位、类型、主题色仍然来自代码预设。RoleType 尤其不由大模型产出——讲解只由教师发声，
// 这个字段一旦交给大模型写，就可能冒出第二个 teacher，直接破坏这条业务规则。
//
// 大模型产出的字段必须在应用层校验后再落库，数据库的长度约束与 CHECK 只负责拦下脏数据、
// 不负责修正：Name 按字符截断到 120、Persona 截断到 255（PostgreSQL 的 varchar 超长是
// 报错而非静默截断，会让整个生成事务回滚），Name 为空或纯空白时回退为预设名称；
// Persona 必须与 Name 一致——名字改了、简介不能还是原来那句；SystemPrompt 虽为 text，
// 也应设长度上限。
//
// Avatar 与 VoiceID 另有一层风险：大模型可能返回不存在的值（编造一个资源路径或音色 ID），
// 数据库只看得到长度，拦不住这种错。应用层必须拿返回值与预设清单比对，对不上就回退为
// 槽位原配的那一套。
//
// 本表只保存这门课当前正在使用的角色，不保留历史版本：教师行自课程创建起一直存在，
// 改音色就是更新这一行；学生行按本次选定的角色集合写入，换了一批角色重新生成时，
// 旧行删除、新行插入。大模型生成的只是「这门课用哪些角色、叫什么、什么人设」，
// 改不到代码里那 6 个预设定义——历史角色仍然是历史角色。
// 讲解只由教师发声，学生行不会被 scene_segments 引用，因此删掉旧行不会打断讲解段落。
//
// 约束（§4.3）：UNIQUE (classroom_id, sort_order)、UNIQUE (classroom_id, agent_key)。
// classroom_id 同时参与这两个复合唯一约束，而 GORM 的 tag 在同一字段上无法声明两个复合索引
// （ParseTagSetting 对重复 key 是覆盖而非追加），因此这两条约束写在 SQL migration 中。
//
// 唯一约束建在 AgentKey 而不是 Avatar 上：预设里两者是 1:1 的，但 Avatar 只是展示属性
// （文件名带 -2 后缀，会换图），AgentKey 才是角色身份，也顺带保证教师唯一。
type ClassroomAgent struct {
	BaseModel

	ClassroomID uint64 `gorm:"column:classroom_id;not null" json:"classroom_id"` // 所属课程；级联删除

	Source   string `gorm:"column:source;type:varchar(32);not null" json:"source"`       // preset（代码预设，教师恒为本值）| auto（大模型重写名称与提示词）
	AgentKey string `gorm:"column:agent_key;type:varchar(80);not null" json:"agent_key"` // 槽位定义 ID，恒为 teacher、assist、clown、curious、note-taker、thinker 之一
	Name     string `gorm:"column:name;type:varchar(120);not null" json:"name"`          // 角色展示名称；auto 下由大模型重写，写入前须按字符截断
	Role     string `gorm:"column:role;type:varchar(120);not null" json:"role"`          // 角色职责/定位，如「主讲」「提问」；跟随槽位固定
	RoleType string `gorm:"column:role_type;type:varchar(32);not null" json:"role_type"` // teacher | assistant | student；由后端按槽位从代码预设取出，不由大模型产出
	Persona  string `gorm:"column:persona;type:varchar(255);not null" json:"persona"`    // 角色人设简介（信息卡正文）；auto 下随 Name 一起重写，写入前须按字符截断

	SystemPrompt string `gorm:"column:system_prompt;type:text;not null" json:"system_prompt"` // 本课程实际使用的完整系统提示词快照；auto 下由大模型重写
	VoiceID      string `gorm:"column:voice_id;type:varchar(120);not null" json:"voice_id"`   // 音色 ID；preset 跟随槽位，auto 由大模型从预设值中挑选，写入前须校验
	Avatar       string `gorm:"column:avatar;type:varchar(255);not null" json:"avatar"`       // 六个默认头像之一的资源路径；preset 跟随槽位，auto 由大模型从预设值中挑选，写入前须校验
	Color        string `gorm:"column:color;type:varchar(16);not null" json:"color"`          // 界面主题色快照，如 #52c41a；跟随槽位固定

	SortOrder *int32 `gorm:"column:sort_order" json:"sort_order"` // 学员之间的排列位次，从 0 开始按预设槽位顺序排；教师不属于该序列，恒为 NULL
}

// TableName 返回表名。
func (ClassroomAgent) TableName() string { return "classroom_agents" }
