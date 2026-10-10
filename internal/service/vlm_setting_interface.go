package service

import (
	"context"

	"narra/pkg/documentparser"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
)

// VLMSettingService 是视觉模型（VLM）配置的 HTTP 面。
//
// 形态与「重排模型」一致：表里可留多条，同一时间只有一条启用（is_enabled 上的部分
// 唯一索引在数据库层保证），文档解析只使用启用中的那条。启用前必须先测试连接 ——
// 让解析拿着一个必然失败的配置上线，只会让每份含图文档都白等一次超时。
//
// 与重排的差异：这里没有需要热更新的常驻运行时（真正的调用发生在解析子进程里，
// 配置在每次解析时按需读取），所以启停与编辑天然即时生效，也没有 LoadActive 这一步。
type VLMSettingService interface {
	// List 返回全部配置，含未测试与已停用的，供设置页展示。
	List(ctx context.Context, ownerID ...uint64) ([]responsedto.VLMSetting, error)

	// Create 新建配置（一律入库、默认不启用）。名称重复返回 409 语义的冲突错误。
	Create(ctx context.Context, input requestdto.VLMSetting, ownerID ...uint64) (*responsedto.VLMSetting, error)

	// Update 全量更新。地址、超时、模型、密钥任一变动，都把配置打回未测试并停用。
	Update(ctx context.Context, id uint64, input requestdto.VLMSetting, ownerID ...uint64) (*responsedto.VLMSetting, error)

	// Delete 删除配置。删的正好是启用中的那条时，视觉理解随之为"无生效配置"。
	Delete(ctx context.Context, id uint64, ownerID ...uint64) error

	// Test 发一次真实视觉探测（64x64 图片 + 要求回复 OK），成功才算可用；
	// 失败会写回状态并停用该配置。
	Test(ctx context.Context, id uint64, ownerID ...uint64) (*responsedto.VLMSettingTestResult, error)

	// TestConnection 用**未保存的表单值**发一次探测（设置页的"测试连接"按钮），
	// 不写任何状态；编辑既有配置时传 ID，API Key 留空则沿用已保存的那把。
	TestConnection(ctx context.Context, input requestdto.VLMSettingProbe, ownerID ...uint64) (*responsedto.VLMSettingTestResult, error)

	// SetEnabled 切换启用状态；未测试通过的配置不允许启用。
	// 启用一条会取消其它配置的启用状态（同事务完成）。
	SetEnabled(ctx context.Context, id uint64, enabled bool, ownerID ...uint64) (*responsedto.VLMSetting, error)

	// CurrentVision 返回当前启用的运行时视觉配置（含解密后的 API Key），
	// 供收录解析链路使用（满足 rag.VisionProvider）。没有启用项时 ok = false ——
	// 那是"视觉理解关闭"，不是错误。
	CurrentVision(ctx context.Context, ownerID ...uint64) (documentparser.VLMConfig, bool, error)
}
