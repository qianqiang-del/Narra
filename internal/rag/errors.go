package rag

import "errors"

// 收录链路里可辨别的失败类别。
//
// 只给"调用方可能想分支处理"的情形建哨兵值，普通的参数校验错误不值得单独一个类型。
// 主要的失败通道仍然是文档表上的状态机（pending → processing → ready / failed）——
// 失败阶段和原因都落在 metadata 里，面向用户的那句话从那里出。
// 这里这几个值是为了让**进程内**能判断自己撞上了哪一类：
// 例如后台跑收录任务时可以据此决定"这份文档还值不值得重试"。
var (
	// ErrEmptyContent 表示解析产物或投递进来的正文是空的，切不出任何切片。
	ErrEmptyContent = errors.New("没有可入库的正文")

	// ErrTooManyChunks 表示切出的切片数超过单篇上限，需要拆成多篇再导入。
	ErrTooManyChunks = errors.New("切片数超过单篇上限")

	// ErrEmbeddingDisabled 表示向量服务没有启用，收录无法进行。
	ErrEmbeddingDisabled = errors.New("向量服务未启用")

	// ErrNoEmbeddingModel 表示库里还没有登记任何可用的向量模型。
	ErrNoEmbeddingModel = errors.New("没有可用的向量模型")

	// ErrEmbeddingMismatch 表示向量服务返回的条数与请求的切片数不符。
	// 这时绝不能按位置硬凑：凑出来的向量会挂到错误的切片上，检索结果全错且看不出来。
	ErrEmbeddingMismatch = errors.New("向量条数与切片数不符")

	// ErrInvalidVector 表示向量为空或含 NaN / Inf。
	// 它们在 pgvector 的文本字面量里会被写成 "NaN" / "+Inf"，被数据库直接拒掉。
	ErrInvalidVector = errors.New("向量内容非法")

	// ErrEmptyQuery 表示检索词是空的。
	// 与 ErrEmptyContent 对偶：那边是"没东西可入库"，这边是"没东西可查"。
	ErrEmptyQuery = errors.New("检索词不能为空")
)
