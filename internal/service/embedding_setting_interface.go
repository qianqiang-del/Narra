package service

import (
	"context"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
	"narra/pkg/config"
)

// EmbeddingSettingService 管理 Embedding 服务配置。
//
// 配置有两份来源，读取时按"用户的优先、缺失回退默认"解析：
//   - 平台默认：来自 config.yaml / 环境变量，只读、不落库，作为用户没配时的兜底；
//   - 用户配置：落 embedding_settings（按账号一行生效），保存即对该账号生效。
//
// 再没有"进程级一份生效配置"：收录与检索通过 CurrentEmbedding 按账号现取，
// 所以 Save 之后不需要热更新任何共享状态。
type EmbeddingSettingService interface {
	// Current 返回该账号当前生效配置的对外结构；没有个人配置时返回空表单，
	// 并用 UsingPlatformDefault 标记"当前在吃平台默认（如果启用了）"。
	Current(ctx context.Context, ownerID uint64) (responsedto.EmbeddingSetting, error)

	// Save 保存该账号的配置，保存后立即生效，不需要重启进程。
	Save(ctx context.Context, ownerID uint64, input requestdto.EmbeddingSetting) (responsedto.EmbeddingSetting, error)

	// Test 用入参里的配置真实请求一次 embedding 服务做连通性校验，
	// 不写库、不影响当前生效配置，成功时返回配置中的向量维度。
	Test(ctx context.Context, ownerID uint64, input requestdto.EmbeddingSetting) (int, error)

	// CurrentEmbedding 返回该账号当前生效的连接配置：用户配置优先，缺失回退平台默认
	// （可能 Enabled = false，表示未配置）。它是 rag.EmbeddingProvider 的实现，
	// 收录与检索在每次使用前现取一次。
	CurrentEmbedding(ctx context.Context, ownerID uint64) (config.EmbeddingConfig, error)
}
