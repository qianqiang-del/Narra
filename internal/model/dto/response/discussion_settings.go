package response

type DiscussionSettings struct {
	ProviderID   uint64 `json:"llm_provider_id"`
	ModelID      string `json:"llm_model_id"`
	ProviderName string `json:"provider_name"`
	Source       string `json:"source"`
	Available    bool   `json:"available"`
}
