package response

import "encoding/json"

type SceneNarrationSegment struct {
	ID         uint64  `json:"id"`
	SceneID    uint64  `json:"scene_id"`
	ContentKey string  `json:"content_key"`
	SortOrder  int32   `json:"sort_order"`
	Text       string  `json:"text"`
	Status     string  `json:"status"`
	AudioPath  *string `json:"audio_path"`
}

type SceneContentResponse struct {
	SceneID uint64          `json:"scene_id"`
	Status  string          `json:"status"`
	Content json.RawMessage `json:"content"`
}

type SceneDetailResponse struct {
	ID           uint64                  `json:"id"`
	SortOrder    int32                   `json:"sort_order"`
	Type         string                  `json:"type"`
	Title        string                  `json:"title"`
	Brief        string                  `json:"brief"`
	Status       string                  `json:"status"`
	Content      json.RawMessage         `json:"content"`
	Narration    []SceneNarrationSegment `json:"narration"`
	ErrorMessage *string                 `json:"error_message"`
}
