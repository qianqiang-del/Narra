// Eino 适配层：把 Client 暴露成 Eino 的 embedding.Embedder。
// 单独一个文件、不改 client.go，是为了让「向量化怎么发请求」和「怎么接进 Eino」
// 两件事分开演进：底层协议和校验归 client.go，框架对接归这里。
// 与 internal/mcp/adapter.go 把 MCP 工具包成 tool.BaseTool 是同一套路。
package embedding

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/embedding"
)

// EinoEmbedder 让 Eino 的 Retriever / Indexer 能直接消费本项目的向量化能力。
//
// 它是薄适配器而不是重实现：请求构造、返回条数校验、索引校验、维度校验
// 全部继续由 Client.Embed 承担。这些校验不是可有可无的装饰——
// 官方 eino-ext 的 OpenAI Embedder 两者都没有（按 len(resp.Data) 建切片、
// 完全忽略返回的 index 字段），上游少返几条或乱序返回时会静默产出
// 「向量与文本错位」的结果，对 RAG 属于无声的数据损坏，所以刻意不换成它。
type EinoEmbedder struct {
	client *Client
}

// 编译期确认适配器满足 Eino 的 Embedder；上游接口签名变动时这里先编译失败。
var _ embedding.Embedder = (*EinoEmbedder)(nil)

// NewEinoEmbedder 用已配置好的 Client 创建适配器。
//
// 注意 Client 是「某个时刻的配置快照」：base_url / model 在构造时就固化了。
// 接入 Eino 时请在使用点用 embedding.Manager 的当前配置现建现用，
// 不要在启动时建一个长期持有，否则设置页改了配置它不会跟着变。
func NewEinoEmbedder(client *Client) (*EinoEmbedder, error) {
	if client == nil {
		return nil, fmt.Errorf("Eino 向量化适配器需要非空的 Client")
	}
	return &EinoEmbedder{client: client}, nil
}

// EmbedStrings 实现 embedding.Embedder，一次调用返回与 texts 等长、顺序一致的向量。
//
// 返回值是 [][]float64 以符合接口要求；数值本身来自 Client.Embed 的 float32 解析结果，
// 与 eino-ext 官方实现（同样先解到 []float32 再逐位转 float64）精度一致，没有额外损失。
//
// texts 为空时沿用 Client.Embed 的报错行为，而不是静默返回空切片：
// 空批次意味着调用方拿不到任何向量，早点失败比让上层拿空结果继续跑更安全。
func (e *EinoEmbedder) EmbedStrings(ctx context.Context, texts []string, opts ...embedding.Option) ([][]float64, error) {
	// 值拷贝：本次调用可能通过 option 覆盖模型名，不能改到共享的 Client 上，
	// 否则并发调用之间会互相污染。Client 只含字符串、整数和 *http.Client，拷贝是安全的。
	client := *e.client
	if model := embedding.GetCommonOptions(nil, opts...).Model; model != nil && strings.TrimSpace(*model) != "" {
		client.model = strings.TrimSpace(*model)
	}

	vectors, err := client.Embed(ctx, texts)
	if err != nil {
		return nil, err
	}

	converted := make([][]float64, len(vectors))
	for index, vector := range vectors {
		row := make([]float64, len(vector))
		for position, value := range vector {
			row[position] = float64(value)
		}
		converted[index] = row
	}
	return converted, nil
}

// GetType 供 Eino 的工具链给组件起展示名（最终呈现为 "NarraOpenAIEmbedding"）。
//
// 刻意不实现 IsCallbacksEnabled：本适配器自己不发回调，让框架按默认行为
// 补 OnStart / OnEnd 才正确；声明成 true 会让框架跳过默认包装、
// 结果一个回调都收不到，比不实现更糟。
func (e *EinoEmbedder) GetType() string { return "NarraOpenAI" }
