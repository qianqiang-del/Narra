package entity

// ClassroomAgent 课堂与角色的关联，对应表 classroom_agents。
//
// 本表只回答一件事：**哪堂课用了哪些角色，各自选了什么音色**。
// 角色本身（名称、定位、人设、头像、主题色）不在这里，去 preset_agents 查。
//
// # 为什么只留 VoiceID
//
// 判断标准是「这个字段确定的东西，有没有固化进产物」：
//
//   - VoiceID 有。它决定了已经合成进 scene_segments.audio_path 的那个声音是谁，
//     音频出来之后就改不回来了。所以它必须每堂课存一份——之后角色池里改了默认音色，
//     已经生成的课不该跟着变。值有两个来源：生成课堂时大模型挑，和用户事后在下拉框
//     里手动更换（目录来自 GET /api/v1/voices）；**重新生成会整个覆盖**，手动修改不留。
//   - 名称 / 定位 / 人设 / 头像 / 主题色没有。它们每次渲染都重新读 preset_agents，
//     改了就是改了，不存在「讲稿里说 A、界面上显示 B」的不一致。
//
// 这些字段原先在本表里各存了一份副本，那是多余的：两份数据靠人工保持同步，
// 漂了也没有东西会发现——前端那份至少还能写条测试比一比，数据库里两处分叉
// 只有运行时才炸。
//
// # 约束
//
// UNIQUE (classroom_id, agent_id)：同一个角色在一堂课里只出现一次。由两个字段上同名的
// uniqueIndex tag 声明，AutoMigrate 建（唯一索引与唯一约束在 PostgreSQL 里等价）。
//
// agent_id 是 ON DELETE RESTRICT：想删一个还被课程引用的角色会被拦下，
// 池子里下架角色应该用 preset_agents.enabled = false。两条外键由下方关联字段上的
// constraint tag 声明。
//
// 这里没有 role_type，所以「每堂课恰好一个教师」**建不出数据库约束**——跨表的
// 部分唯一索引做不到。这条规则只能由应用层在生成课程时保证，也是本设计里
// 丢掉的最硬的一条保障。
//
// 同样地，本表也没有 sort_order：圆桌上角色的排列顺序取 preset_agents.sort_order。
// 将来若需要每堂课单独调顺序，再加列。
type ClassroomAgent struct {
	BaseModel

	ClassroomID uint64 `gorm:"column:classroom_id;not null;uniqueIndex:classroom_agents_classroom_id_agent_id_key" json:"classroom_id"`                             // 所属课程；级联删除
	AgentID     uint64 `gorm:"column:agent_id;not null;uniqueIndex:classroom_agents_classroom_id_agent_id_key;index:idx_classroom_agents_agent_id" json:"agent_id"` // 指向 preset_agents.id

	// Classroom / Agent 仅供 AutoMigrate 建外键（分别 ON DELETE CASCADE / RESTRICT）。
	// 业务代码禁止给它们赋值或 Preload：赋了非空值再保存本行，GORM 会连带 upsert 目标表的行。
	Classroom *Classroom   `gorm:"foreignKey:ClassroomID;constraint:classroom_agents_classroom_id_fkey,OnDelete:CASCADE" json:"-"`
	Agent     *PresetAgent `gorm:"foreignKey:AgentID;constraint:classroom_agents_agent_id_fkey,OnDelete:RESTRICT" json:"-"`

	VoiceID string `gorm:"column:voice_id;type:varchar(120);not null" json:"voice_id"` // 本课程为这个角色选定的音色；来源见上面的说明，写入前须用 service.IsValidVoiceID 校验
}

// TableName 返回表名。
func (ClassroomAgent) TableName() string { return "classroom_agents" }
