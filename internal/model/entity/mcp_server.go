package entity

import "time"

// MCPServer MCP 服务配置实体。
type MCPServer struct {
	BaseModel
	ServerID         string        `gorm:"column:server_id;type:varchar(64);not null;unique;comment:服务稳定标识，全局唯一，前后端契约" json:"server_id"`
	Name             string        `gorm:"column:name;type:varchar(128);not null;comment:服务展示名" json:"name"`
	Enabled          bool          `gorm:"column:enabled;not null;default:true;comment:是否启用该服务" json:"enabled"`
	Required         bool          `gorm:"column:required;not null;default:false;comment:是否为必装服务；必装项不允许在界面上禁用" json:"required"`
	Transport        string        `gorm:"column:transport;type:varchar(32);not null;default:streamable_http;comment:传输方式，默认 streamable_http" json:"transport"`
	Endpoint         string        `gorm:"column:endpoint;type:varchar(512);not null;comment:服务地址，HTTP 端点或本地启动命令" json:"endpoint"`
	APIKey           string        `gorm:"column:api_key;type:varchar(512);comment:访问该服务的密钥；明文不落库，接口也不返回" json:"-"`
	AuthEnv          string        `gorm:"column:auth_env;type:varchar(128);comment:存放密钥的环境变量名" json:"auth_env"`
	StartupTimeout   time.Duration `gorm:"column:startup_timeout;not null;comment:启动超时，单位纳秒（Go time.Duration，10000000000 即 10 秒）" json:"startup_timeout"`
	DiscoveryTimeout time.Duration `gorm:"column:discovery_timeout;not null;comment:拉取工具列表的超时，单位纳秒" json:"discovery_timeout"`
	CallTimeout      time.Duration `gorm:"column:call_timeout;not null;comment:单次工具调用的超时，单位纳秒" json:"call_timeout"`
	EnabledTools     []string      `gorm:"column:enabled_tools;type:jsonb;not null;default:'[]';serializer:json;comment:允许挂给模型的工具名（该服务的本地名）；空数组表示不过滤，本服务全部工具都启用" json:"enabled_tools"`
	SortOrder        int           `gorm:"column:sort_order;not null;default:0;comment:列表展示顺序，越小越靠前" json:"sort_order"`
}

func (MCPServer) TableName() string { return "mcp_servers" }
