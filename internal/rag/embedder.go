package rag

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Embedder 是本模块对向量化能力的最小依赖面。
//
// 定义接口而不直接依赖 *embedding.Client，是为了让收录链路的测试能注入一个确定性的桩
// （例如把每段文本映射成一个可预测的向量）：否则测一次切分与入库就要真的联网调一次
// 向量服务，测试会退化成"看上游今天通不通"。*embedding.Client 的 Embed 签名与它完全一致。
type Embedder interface {
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
}

// embedderFactory 现建一个 Embedder。
//
// 做成工厂而不是在构造时持有一个实例：Client 是配置快照（base_url 和 model 在构造时
// 就固化了），启动时建一个长期持有，设置页改了地址或模型之后它不会跟着变，
// 会静默把向量写到错误的模型下。所以每次收录都按当前生效配置现建一个。
//
// 同时它也是这个包唯一的注入点：测试用一个返回桩的工厂替换它。
type embedderFactory func() (Embedder, error)

// embedBatchSize 每次向量化请求携带的切片数。
//
// 批量是必须的：逐条请求会把一篇文档的向量化变成几百次网络往返。
// 但批量也不能无限大 —— 上游对单次请求的 token 总量有硬限制，而切片本身就有几百字。
// 16 片 × 800 字在多数 OpenAI 兼容服务的单请求额度内仍有余量。
const embedBatchSize = 16

// embedInBatches 分批向量化，返回与 chunks 等长、顺序一致的向量。
//
// 返回值保持 []float32 而不是直接转成 pgvector 文本：维度校验和入库前的有限性检查
// 都要看原始数值，提前转成字符串就只能再解析回来。
func embedInBatches(ctx context.Context, embedder Embedder, chunks []Chunk) ([][]float32, error) {
	vectors := make([][]float32, len(chunks))

	for start := 0; start < len(chunks); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(chunks) {
			end = len(chunks)
		}

		inputs := make([]string, 0, end-start)
		for _, chunk := range chunks[start:end] {
			inputs = append(inputs, chunk.Content)
		}

		batch, err := embedder.Embed(ctx, inputs)
		if err != nil {
			return nil, fmt.Errorf("第 %d~%d 个切片向量化失败: %w", start+1, end, err)
		}
		if len(batch) != len(inputs) {
			return nil, fmt.Errorf("%w: 第 %d~%d 个切片请求 %d 条，上游返回 %d 条",
				ErrEmbeddingMismatch, start+1, end, len(inputs), len(batch))
		}
		for index, vector := range batch {
			vectors[start+index] = vector
		}
	}
	return vectors, nil
}

// vectorLiteral 把向量转成 pgvector 的文本格式，如 "[0.1,0.2]"。
//
// 位宽用 32：Client 解析出来的就是 float32，按 64 位格式化会输出一串实际不存在的精度
// （0.10000000149011612），既占空间又没有信息量。
//
// NaN 与 Inf 必须在这里拦掉：它们在 SQL 文本里会被写成 "NaN" / "+Inf"，
// pgvector 直接拒绝，报出来的是一个看不出根因的语法错误。上游服务返回坏向量是
// 真实发生过的事，落到库里就是一批永远排序异常的向量，很难查。
func vectorLiteral(vector []float32) (string, error) {
	if len(vector) == 0 {
		return "", fmt.Errorf("%w: 向量为空", ErrInvalidVector)
	}
	values := make([]string, len(vector))
	for index, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return "", fmt.Errorf("%w: 第 %d 维不是有限数（NaN 或 Inf）", ErrInvalidVector, index+1)
		}
		values[index] = strconv.FormatFloat(float64(value), 'g', -1, 32)
	}
	return "[" + strings.Join(values, ",") + "]", nil
}
