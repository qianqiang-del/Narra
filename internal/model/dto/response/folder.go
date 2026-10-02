package response

import "time"

type Folder struct {
	ID             uint64    `json:"id"`
	Name           string    `json:"name"`
	ClassroomCount int64     `json:"classroom_count"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type FolderClassroom struct {
	ID        uint64    `json:"id"`
	Title     string    `json:"title"`
	Mode      string    `json:"mode"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type FolderDetail struct {
	Folder
	Classrooms []FolderClassroom `json:"classrooms"`
}
