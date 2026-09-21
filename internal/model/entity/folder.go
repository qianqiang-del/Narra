package entity

// Folder 课程文件夹，对应表 folders（设计文档 §4.1）。
//
// 删除规则（§6）：文件夹硬删除，只删除本行；该文件夹下所有课程的
// classrooms.folder_id 置为 NULL，课程变为未归档状态，不会被删除。
//
// V1 只有根目录一层，不需要 parent_id；需要目录树时见 §9.2。
type Folder struct {
	BaseModel

	Name string `gorm:"column:name;type:varchar(120);not null;comment:文件夹名称" json:"name"` // 文件夹名称
}

// TableName 返回表名。
func (Folder) TableName() string { return "folders" }
