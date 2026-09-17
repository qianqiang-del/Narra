package repository

import (
	"context"
	"fmt"

	"narra/internal/model/entity"
)

// EmbeddingModelRepository 负责向量模型表 embedding_models 的持久化。
//
// 这张表是知识库向量的合法模型来源：knowledge_embeddings.model_id 外键指向它，
// 而设置页写的是另一张表 embedding_settings（记录"怎么连服务"）。
// 两张表语义相邻却没有关联，桥接就落在这里 —— 保存一次生效配置，
// 就保证这里有一行与之对应、且全局唯一的默认模型。
//
// 写入口只开了一个 EnsureDefault：模型行的字段全部由当前生效配置推导，
// 没有"单独改某一列"的场景，所以不提供 Update，避免出现两处都能改同一行的局面。
type EmbeddingModelRepository interface {
	// EnsureDefault 保证 name 对应的模型行存在、字段与入参一致、并成为唯一默认模型。
	// 返回落库后的模型（含 ID），供写入知识库向量时引用。
	//
	// 同名模型的维度发生变化且该模型下已有向量时，返回 *DimensionsMismatchError
	// 而不是就地覆盖 —— 理由见该类型的注释。
	EnsureDefault(ctx context.Context, model entity.EmbeddingModel) (*entity.EmbeddingModel, error)
}

// DimensionsMismatchError 表示同名模型请求的维度与已登记值不一致。
//
// 仓储只陈述事实（含受影响的数据量），翻译成给用户看的提示放在业务层：
// 改维度这件事本身不非法，非法的是在已经有向量的情况下改。
type DimensionsMismatchError struct {
	ModelName string // 模型名
	ModelID   uint64 // 已登记行的主键
	Recorded  int32  // 已登记的维度
	Requested int32  // 本次请求的维度
	Vectors   int64  // 该模型下已有的向量数量
}

// Error 实现 error 接口，用于日志；用户可见的措辞由业务层组装。
func (e *DimensionsMismatchError) Error() string {
	return fmt.Sprintf(
		"embedding model dimensions mismatch: model=%s id=%d recorded=%d requested=%d vectors=%d",
		e.ModelName, e.ModelID, e.Recorded, e.Requested, e.Vectors,
	)
}
