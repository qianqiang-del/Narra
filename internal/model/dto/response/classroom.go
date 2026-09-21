package response

import "time"

// Classroom 是课堂的对外视图，受理返回与轮询查询共用。
type Classroom struct {
	ID              uint64    `json:"id"`
	FolderID        *uint64   `json:"folder_id"`
	Title           string    `json:"title"`
	Requirement     string    `json:"requirement"`
	Mode            string    `json:"mode"`
	Status          string    `json:"status"`
	GenerationError *string   `json:"generation_error"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
