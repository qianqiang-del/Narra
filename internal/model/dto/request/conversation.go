package request

// CreateConversation 创建课堂中的一条对话。
//
// 当前播放页只创建 discussion；type 仍由请求携带，是为了让同一资源接口将来能
// 承接 qa / lecture，而不让前端改 URL。
type CreateConversation struct {
	Title string `json:"title"`
	Type  string `json:"type"`
}
