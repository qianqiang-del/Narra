package rag

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	einoembedding "github.com/cloudwego/eino/components/embedding"
	"go.uber.org/zap"

	"narra/internal/model/entity"
	"narra/pkg/config"
	"narra/pkg/embedding"
	"narra/pkg/logger"
)

// Embedder 是本模块对向量化能力的依赖面。
//
// 它**就是** Eino 的 embedding.Embedder（类型别名，不是另抄一份签名）：生产路径由
// pkg/embedding 的 EinoEmbedder 实现，将来把收录或检索接进 Eino 的 indexer / retriever 时，
// 同一个值可以直接传进去，不用再包一层适配。
//
// 之所以仍然"接口 + 工厂"而不是直接持有 *embedding.EinoEmbedder：测试要能注入一个
// 确定性的桩（把每段文本映射成可预测的向量），否则测一次切分与入库就要真的联网调一次
// 向量服务，测试会退化成"看上游今天通不通"。注入点是下面的 embedderFactory。
type Embedder = einoembedding.Embedder

// 编译期确认生产实现满足本模块的依赖面；上游接口签名变动时这里先编译失败。
var _ Embedder = (*embedding.EinoEmbedder)(nil)

// embedderFactory 按"这次要用哪个模型"现建一个 Embedder。
//
// 三个约束叠在一起才长成这样：
//
//   - **现建而不是持有**：Client 是配置快照（base_url 与 model 在构造时就固化了），
//     启动时建一个长期持有，设置页改了地址或模型之后它不会跟着变，会静默把向量写到
//     错误的模型下（见 pkg/embedding/eino.go 的提醒）。
//   - **收模型行而不是只读全局配置**：查询串与切片必须用**库里向量所属的那个模型**去
//     向量化。两者一旦漂移（改了设置却没重建切片），相似度照样算得出来、不报任何错，
//     只是排序全是噪声 —— 这类静默劣化最难查。
//   - **可换成桩**：它是本包唯一的注入点，测试用它把向量化变成确定性实现。
type embedderFactory func(model *entity.EmbeddingModel) (Embedder, error)

// newModelEmbedderFactory 是生产路径的工厂：按模型行 + 当前生效的连接配置现建 Eino 适配器。
//
// 收录与检索共用它，"用哪个模型、连哪个地址、超时多久"因此只有一处判断。
func newModelEmbedderFactory(manager *embedding.Manager) embedderFactory {
	return func(model *entity.EmbeddingModel) (Embedder, error) {
		client, err := embedding.NewClient(modelEmbedderConfig(manager, model))
		if err != nil {
			return nil, fmt.Errorf("向量服务配置不可用: %w", err)
		}
		return embedding.NewEinoEmbedder(client)
	}
}

// modelEmbedderConfig 把"库里的模型行"与"当前生效的连接配置"拼成一份向量化配置。
//
// 连接信息（密钥、超时）来自全局配置；模型身份（名字、维度、地址）以模型行为准 ——
// 模型行上带了自己的 base_url 时也以它为准：同一个密钥配到不同网关的场景下，
// 全局配置里的地址可能根本托管不了这个模型。
//
// 包级可见是为了能被单测直接验（纯函数，不建客户端、不发请求）。
func modelEmbedderConfig(manager *embedding.Manager, model *entity.EmbeddingModel) config.EmbeddingConfig {
	cfg := manager.Config()
	if model == nil {
		return cfg
	}
	cfg.Model = model.Name
	cfg.Dimensions = int(model.Dimensions)
	if model.BaseURL != nil && strings.TrimSpace(*model.BaseURL) != "" {
		cfg.BaseURL = *model.BaseURL
	}
	return cfg
}

// embedBatchSize 每次向量化请求携带的切片数。
//
// 批量是必须的：逐条请求会把一篇文档的向量化变成几百次网络往返。
// 但批量也不能无限大 —— 上游对单次请求的 token 总量有硬限制，而切片本身就有几百字。
// 16 片 × 800 字在多数 OpenAI 兼容服务的单请求额度内仍有余量。
const embedBatchSize = 16

// embedBatchAttempts 单批向量化的总尝试次数（含第一次）。
//
// 3 次是"挡掉偶发抖动"与"别把故障时间拉长"之间的折中：一篇文档可能有几十批，
// 每批都重试很多次只会让真故障看起来像卡住。退避后的额外等待每批最多几秒。
const embedBatchAttempts = 3

// embedRetryBaseDelay 重试的基准间隔：第 n 次重试等 base << (n-1)，默认 1s、2s。
//
// 声明成变量而不是常量，是为了让测试把它压到毫秒级 —— 否则一个重试用例要跑好几秒。
// 生产路径不会改它。
var embedRetryBaseDelay = time.Second

// embedInBatches 分批向量化，返回与 chunks 等长、顺序一致的向量。
//
// 返回值保持数值切片而不是直接转成 pgvector 文本：维度校验和入库前的有限性检查
// 都要看原始数值，提前转成字符串就只能再解析回来。
//
// 每一批内部做有限次重试（见 embedBatchWithRetry）：收录一篇文档可能是几十批请求，
// 任何一批撞上限流或网络抖动，整篇就会失败，而用户重试要从解析重新来过。
func embedInBatches(ctx context.Context, embedder Embedder, chunks []Chunk) ([][]float64, error) {
	vectors := make([][]float64, len(chunks))

	for start := 0; start < len(chunks); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(chunks) {
			end = len(chunks)
		}

		inputs := make([]string, 0, end-start)
		for _, chunk := range chunks[start:end] {
			inputs = append(inputs, chunk.Content)
		}

		batch, err := embedBatchWithRetry(ctx, embedder, inputs)
		if err != nil {
			return nil, fmt.Errorf("第 %d~%d 个切片向量化失败: %w", start+1, end, err)
		}
		for index, vector := range batch {
			vectors[start+index] = vector
		}
	}
	return vectors, nil
}

// embedBatchWithRetry 对一个批次做有限次重试，并校验返回的条数。
//
// 只重试明确可恢复的错误：429、5xx、网络超时与连接错误（见 isRetryableEmbedError）。
// 参数错误、向量条数或维度不对这类"再来一次也一样"的失败直接返回，不浪费时间。
// 上层 ctx 被取消（关停）时立刻放弃，连退避等待也要能被打断。
//
// 调用的是 Eino 的 EmbedStrings（Embedder 是它的别名）；本模块不用它的 option，
// 模型由工厂在构造时固化 —— 这比每次调用都传一遍更难写错。
func embedBatchWithRetry(ctx context.Context, embedder Embedder, inputs []string) ([][]float64, error) {
	var lastErr error

	for attempt := 0; attempt < embedBatchAttempts; attempt++ {
		if attempt > 0 {
			delay := embedRetryBaseDelay << (attempt - 1)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		batch, err := embedder.EmbedStrings(ctx, inputs)
		if err == nil {
			if len(batch) != len(inputs) {
				// 条数不符是契约被破坏：同一批再问一次只会得到同样的结果，不重试。
				return nil, fmt.Errorf("%w: 请求 %d 条，上游返回 %d 条",
					ErrEmbeddingMismatch, len(inputs), len(batch))
			}
			return batch, nil
		}
		lastErr = err

		if ctx.Err() != nil {
			// 关停/取消：重试没有意义，立刻返回而不是等完退避。
			return nil, ctx.Err()
		}
		if !isRetryableEmbedError(err) {
			return nil, err
		}
		logger.Warn("单批向量化失败，准备重试",
			zap.Int("attempt", attempt+1),
			zap.Int("attempts", embedBatchAttempts),
			zap.Int("inputs", len(inputs)),
			zap.Error(err),
		)
	}
	return nil, lastErr
}

// isRetryableEmbedError 判断一个错误值不值得重试。
//
// 两类算：错误自己声明可重试的（embedding.HTTPError 的 429 / 5xx），以及网络层错误
// （超时、连接被拒/重置、DNS 抖动）—— 后者都是"再试一次可能就好"。
// 其余一律不重试：4xx 参数错误、条数/维度不符，重试只会浪费时间。
func isRetryableEmbedError(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var retryable interface{ Retryable() bool }
	if errors.As(err, &retryable) {
		return retryable.Retryable()
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// vectorLiteral 把向量转成 pgvector 的文本格式，如 "[0.1,0.2]"。
//
// 位宽用 32：pgvector 的 vector 列本身是单精度（float4），按 64 位格式化会输出一串
// 最终会被数据库丢掉的精度（0.10000000149011612），既占空间又没有信息量。
// 也正因为列是单精度，Eino 适配器返回的 float64 在这里按 32 位落地不引入额外损失 ——
// 那些数值本来就从 Client 的 float32 解析结果逐位转上来的。
//
// NaN 与 Inf 必须在这里拦掉：它们在 SQL 文本里会被写成 "NaN" / "+Inf"，
// pgvector 直接拒绝，报出来的是一个看不出根因的语法错误。上游服务返回坏向量是
// 真实发生过的事，落到库里就是一批永远排序异常的向量，很难查。
func vectorLiteral(vector []float64) (string, error) {
	if len(vector) == 0 {
		return "", fmt.Errorf("%w: 向量为空", ErrInvalidVector)
	}
	values := make([]string, len(vector))
	for index, value := range vector {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return "", fmt.Errorf("%w: 第 %d 维不是有限数（NaN 或 Inf）", ErrInvalidVector, index+1)
		}
		values[index] = strconv.FormatFloat(value, 'g', -1, 32)
	}
	return "[" + strings.Join(values, ",") + "]", nil
}
