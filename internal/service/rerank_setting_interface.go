package service

import (
	"context"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
)

// RerankSettingService 是重排服务配置的 HTTP 面。
//
// 与「大模型」配置同一种形态：表里可以留多条，同一时间只有一条启用（由 is_enabled 上的
// 部分唯一索引在数据库层保证），检索精排只使用启用中的那条。启用前必须先测试连接——
// 让精排拿着一个必然失败的配置上线，比不精排更糟。
type RerankSettingService interface {
	// List 返回全部配置，含未测试与已停用的，供设置页展示。
	List(ctx context.Context, ownerID ...uint64) ([]responsedto.RerankSetting, error)

	// Create 新建配置（一律入库、默认不启用）。名称重复返回 409 语义的冲突错误。
	Create(ctx context.Context, input requestdto.RerankSetting, ownerID ...uint64) (*responsedto.RerankSetting, error)

	// Update 全量更新。地址、超时、模型、密钥任一变动，都把配置打回未测试并停用。
	Update(ctx context.Context, id uint64, input requestdto.RerankSetting, ownerID ...uint64) (*responsedto.RerankSetting, error)

	// Delete 删除配置。删的正好是启用中的那条时，精排随之为"无生效配置"。
	Delete(ctx context.Context, id uint64, ownerID ...uint64) error

	// Test 发一次真实重排探测（相关内容 + 无关内容），成功才算可用；
	// 失败会写回状态并停用该配置。
	Test(ctx context.Context, id uint64, ownerID ...uint64) (*responsedto.RerankSettingTestResult, error)

	// SetEnabled 切换启用状态；未测试通过的配置不允许启用。
	// 启用一条会取消其它配置的启用状态（同事务完成）。
	SetEnabled(ctx context.Context, id uint64, enabled bool, ownerID ...uint64) (*responsedto.RerankSetting, error)

	// LoadActive 在启动时把持久化的"启用配置"灌进检索侧运行时；没有启用记录时
	// 关闭精排并返回 nil。读到记录却还原失败（密钥解不开、参数非法）才返回错误，
	// 那是真的配置损坏，启动时就暴露比运行时才炸好。
	LoadActive(ctx context.Context) error
}
