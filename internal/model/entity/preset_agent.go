package entity

// 角色大类 preset_agents.role_type。
//
// 由人工在角色池里指定，**不由大模型产出**：讲解只由教师发声，这个字段一旦交给
// 大模型写，就可能冒出第二个 teacher，直接破坏那条业务规则。
//
// 它同时决定两件事：用哪一份角色层提示词（internal/agent/role.go），
// 以及随机挑选时的名额约束（teacher 恰 1、assistant 至多 1、其余为学生）。
//
// 改这三个值时记得两边一起改：这里，和 RoleType 字段上的 check tag。
const (
	PresetAgentRoleTypeTeacher   = "teacher"
	PresetAgentRoleTypeAssistant = "assistant"
	PresetAgentRoleTypeStudent   = "student"
)

// PresetAgent 角色池里的一个角色，对应表 preset_agents。
//
// 这是**角色池**：所有人共用的可选角色清单，由人工维护，不随课程变化。
// 「某堂课实际用了哪些角色、各自选了什么音色」记在 classroom_agents 里，
// 那张表只存指向本表的外键，不再复制这里的字段。
//
// # 为什么角色在数据库而不在代码里
//
// 角色原来写死在 internal/agent/role.go。搬进数据库是为了让「自动生成角色」可控：
// 大模型**不是造角色，是从这个池子里挑角色**。它最坏能返回一个不存在的 id，
// 比对一下就丢掉了，不会编造头像路径或音色 ID，也不会凭空造出第二个教师。
//
// 池子由人工插入，可以一直加。加一个角色只需要填这里的字段，不用动代码——
// 因为提示词不在这里，见下。
//
// # 展示元数据与提示词的分工
//
// 本表存展示元数据与 Persona，**不存系统提示词**。提示词由
// internal/agent/prompts/roles/*.md 按 RoleType 拼装：同一 RoleType 的角色共用一份
// 行为规范，个体差别由 Persona 承载（它是提示词里 {{persona}} 那个槽位）。
//
// 这样做的好处是加角色不用写提示词，也不会因为漏写而让角色变成哑巴。
// 代价是同一 RoleType 的角色行为规则完全相同，只有 Persona 不同——要更细的差异，
// 目前只能靠把 Persona 写好。
//
// # Persona 要当两样东西用
//
// 它既是前端信息卡的展示正文，又是提示词里的人设段。所以它既要给人看，也要真的
// 能当行为指令用：写「课堂里的开心果」这种宣传语会削弱提示词，写「说话短促，
// 爱用反问，从不主动给答案」才有用。定位是「这个角色怎么说话」，不是「是什么」。
//
// # 约束
//
// UNIQUE (agent_key) 与 UNIQUE (sort_order) 由字段上的 unique tag 声明；
// CHECK role_type 与 CHECK sort_order >= 0 由字段上的 check tag 声明。
// 全部归 AutoMigrate 建，同一个约束**不能**再在 SQL migration 里声明：
// AutoMigrate 每次启动都会对账，发现库里唯一而实体上没标 unique，就判定这条约束是多余的
// 并去删它——删除用的还是 GORM 自己算的名字，删不掉就直接 panic。见 migrations/README.md。
//
// 删角色要当心：classroom_agents.agent_id 是 ON DELETE RESTRICT，
// 还被课程引用的角色删不掉。下架应该用 Enabled = false，不是删行。
type PresetAgent struct {
	BaseModel

	AgentKey string `gorm:"column:agent_key;type:varchar(80);not null;unique;comment:角色稳定标识，前后端契约，如 teacher / clown；全局唯一，改它等于换了一个角色" json:"agent_key"` // 稳定标识，前后端契约，如 teacher、clown；改它等于换了一个角色

	Name     string `gorm:"column:name;type:varchar(120);not null;comment:展示名，如「陈老师」" json:"name"`                                                                                                                                                  // 展示名，如「陈老师」
	Role     string `gorm:"column:role;type:varchar(120);not null;comment:展示定位，如「主讲」「质疑」" json:"role"`                                                                                                                                              // 展示定位，如「主讲」「质疑」
	RoleType string `gorm:"column:role_type;type:varchar(32);not null;check:preset_agents_role_type_check,role_type IN ('teacher', 'assistant', 'student');comment:角色大类，取值 teacher / assistant / student；决定用哪份角色层提示词，也决定随机挑选时的名额" json:"role_type"` // teacher | assistant | student；决定用哪份角色层提示词，也决定挑选名额

	Persona string `gorm:"column:persona;type:varchar(1000);not null;comment:人设与说话风格；既是前端信息卡正文，也是提示词里的 {{persona}} 槽位" json:"persona"` // 人设与说话风格；前端信息卡正文 + 提示词里的 {{persona}}

	Avatar  string `gorm:"column:avatar;type:varchar(255);not null;comment:头像资源路径，指向 frontend/public/avatars 下的文件" json:"avatar"` // 头像资源路径，指向 frontend/public/avatars 下的文件
	Color   string `gorm:"column:color;type:varchar(16);not null;comment:界面主题色，如 #722ed1" json:"color"`                           // 界面主题色，如 #722ed1
	VoiceID string `gorm:"column:voice_id;type:varchar(120);not null;comment:默认音色 ID，取值必须在语音目录内" json:"voice_id"`                 // 默认音色 ID，取值必须在 service.IsValidVoiceID 的目录内

	SortOrder int32 `gorm:"column:sort_order;not null;unique;check:preset_agents_sort_order_check,sort_order >= 0;comment:前端角色列表的展示顺序；唯一，否则顺序不确定" json:"sort_order"` // 前端角色列表的展示顺序；唯一，否则顺序不确定
	Enabled   bool  `gorm:"column:enabled;not null;default:true;comment:是否可被挑中；下架用 false，不要删行" json:"enabled"`                                                       // 是否可被挑中；下架用 false
}

// TableName 返回表名。
func (PresetAgent) TableName() string { return "preset_agents" }
