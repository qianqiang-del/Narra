package response

// MCPServerItem MCP 服务列表项。
type MCPServerItem struct {
	ID               uint64   `json:"id"`
	ServerID         string   `json:"server_id"`
	Name             string   `json:"name"`
	Enabled          bool     `json:"enabled"`
	Required         bool     `json:"required"`
	Transport        string   `json:"transport"`
	Endpoint         string   `json:"endpoint"`
	HasAPIKey        bool     `json:"has_api_key"`
	StartupTimeout   string   `json:"startup_timeout"`
	DiscoveryTimeout string   `json:"discovery_timeout"`
	CallTimeout      string   `json:"call_timeout"`
	EnabledTools     []string `json:"enabled_tools"`
	SortOrder        int      `json:"sort_order"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
}

// MCPServerTestResult 测试连接结果。
type MCPServerTestResult struct {
	Success bool     `json:"success"`
	Message string   `json:"message"`
	Tools   []string `json:"tools,omitempty"`
}
