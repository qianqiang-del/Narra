package service

import (
	"context"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
)

// EmbeddingSettingService 管理当前生效的 Embedding 服务配置。
// 配置有两份副本：数据库里的持久化记录（重启后靠它恢复），
// 和进程内 manager 持有的运行时配置（向量化请求实际读的那份）。
// 读接口只看 manager，写接口负责把两份一起改掉。
type EmbeddingSettingService interface {
	// LoadActive 在服务启动时把库里保存的配置加载进 manager；
	// 没有保存过记录就沿用配置文件里的默认值。
	LoadActive(ctx context.Context) error

	// Current 返回当前生效配置的对外结构，密钥只暴露"是否已配置"。
	Current(ctx context.Context) (responsedto.EmbeddingSetting, error)

	// Save 保存配置并让它立即生效，不需要重启进程。
	Save(ctx context.Context, input requestdto.EmbeddingSetting) (responsedto.EmbeddingSetting, error)

	// Test 用入参里的配置真实请求一次 embedding 服务做连通性校验，
	// 不写库、不影响当前生效配置，成功时返回配置中的向量维度。
	Test(ctx context.Context, input requestdto.EmbeddingSetting) (int, error)
}
