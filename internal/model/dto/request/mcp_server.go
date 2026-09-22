package request

// MCPServer 是创建 MCP 服务配置时前端提交的请求体。
type MCPServer struct {
	ServerID         string   `json:"server_id"`
	Name             string   `json:"name"`
	Enabled          bool     `json:"enabled"`
	Required         bool     `json:"required"`
	Transport        string   `json:"transport"`
	Endpoint         string   `json:"endpoint"`
	APIKey           string   `json:"api_key"`
	AuthEnv          string   `json:"auth_env"`
	StartupTimeout   string   `json:"startup_timeout"`
	DiscoveryTimeout string   `json:"discovery_timeout"`
	CallTimeout      string   `json:"call_timeout"`
	EnabledTools     []string `json:"enabled_tools"`
	SortOrder        int      `json:"sort_order"`
}

// MCPServerUpdate 是更新 MCP 服务配置时前端提交的请求体。
// 指针字段：nil 表示不修改，有值才更新。
type MCPServerUpdate struct {
	Enabled      *bool     `json:"enabled,omitempty"`
	EnabledTools *[]string `json:"enabled_tools,omitempty"`
}
