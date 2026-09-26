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

	// ErrRecoveryInputMissing 表示恢复所需的材料全都不在了：原始文件、正文、切片一个都没有。
	// 它是回退链的终点（见 ResolveRecoveryStage）：能退到哪一步就用哪一步，
	// 连原文件都没了才是真的无路可走，只能请用户重新上传。
	ErrRecoveryInputMissing = errors.New("这次收录的输入已经全部丢失，请重新上传")

	// ErrIngestQueueFull 表示收录队列已经达到容量上限（pending + processing 行数），
	// 此刻不再接受新的文件上传或重试入队。
	//
	// 它是"等一会儿再来"而不是"参数错了"：接口层据此翻成 409，批量上传里它只让
	// 当前这个文件被标成 rejected，同批已经入队的文件不受影响。
	ErrIngestQueueFull = errors.New("收录队列已满，请等已有任务处理完再试")
)
