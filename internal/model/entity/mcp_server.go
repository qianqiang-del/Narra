package entity

import "time"

// MCPServer MCP 服务配置实体。
type MCPServer struct {
	BaseModel
	ServerID         string        `gorm:"column:server_id;type:varchar(64);not null;unique" json:"server_id"`
	Name             string        `gorm:"column:name;type:varchar(128);not null" json:"name"`
	Enabled          bool          `gorm:"column:enabled;not null;default:true" json:"enabled"`
	Required         bool          `gorm:"column:required;not null;default:false" json:"required"`
	Transport        string        `gorm:"column:transport;type:varchar(32);not null;default:streamable_http" json:"transport"`
	Endpoint         string        `gorm:"column:endpoint;type:varchar(512);not null" json:"endpoint"`
	APIKey           string        `gorm:"column:api_key;type:varchar(512)" json:"-"`
	AuthEnv          string        `gorm:"column:auth_env;type:varchar(128)" json:"auth_env"`
	StartupTimeout   time.Duration `gorm:"column:startup_timeout;not null" json:"startup_timeout"`
	DiscoveryTimeout time.Duration `gorm:"column:discovery_timeout;not null" json:"discovery_timeout"`
	CallTimeout      time.Duration `gorm:"column:call_timeout;not null" json:"call_timeout"`
	SortOrder        int           `gorm:"column:sort_order;not null;default:0" json:"sort_order"`
}

func (MCPServer) TableName() string { return "mcp_servers" }
