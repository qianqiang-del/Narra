package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
	"narra/pkg/logger"
)

// knowledge_tool.go 内置工具 rag_retrieve：把知识库在线检索挂给 Eino agent。
//
// 它与 adapter.go 里的 einoTool 是两条路：那些工具来自远端 MCP server
// （调用要经 Manager.CallTool 走网络回源），这一个由本服务自己实现，直接调知识库检索。
// 之所以仍放在 internal/mcp，是因为它对外的身份就是"工具"——agent 侧只认
// Manager.EinoTools 这一个来源，多一条来源就要多一处注入。
//
// 契约见 docs/modules/agent-mcp-tools.md：{query, top_k} → {results: [{content, source, score}]}。
// 契约里的 classroom_id 没有实现：知识库是全局资源、不关联课程（见 docs/rag-database.md），
// 收下一个没有语义的参数，只会让模型以为自己能按课程过滤。
const knowledgeRetrieveToolName = "rag_retrieve"

// KnowledgeSearcher 是本工具对检索能力的最小依赖面，*service.KnowledgeService 满足它。
//
// 只留一个方法：工具是薄适配层，检索词的校验、两路召回、融合与降级都在服务层与
// internal/rag 里，这里不该看得见它们（与 http 处理器只认 service 是同一个理由）。
// 用服务层的 DTO 而不是自定义结构：它就是"另一个接口层"，口径应当与 HTTP 面一致。
type KnowledgeSearcher interface {
	Retrieve(ctx context.Context, input requestdto.KnowledgeRetrieve) (responsedto.KnowledgeRetrieveResult, error)
}

// knowledgeRetrieveArgs 是工具入参，字段名就是模型要填的 JSON 键。
type knowledgeRetrieveArgs struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k"`
}

// knowledgeRetrieveHit 是工具出参里的一条命中。
//
// 只带契约里的三个字段：工具结果会原样进入下一轮上下文，chunk_id、document_id、
// 相似度这些对模型没有用的字段要一个不落地剔掉 —— 每次检索多出来的 token 都是钱，
// 而且无关字段会诱导模型去引用它们（"根据切片 12 的相似度 0.83"这种废话）。
type knowledgeRetrieveHit struct {
	Content string  `json:"content"` // 命中的原文切片
	Source  string  `json:"source"`  // 来源标识（原始文件名等），引用时用它
	Score   float64 `json:"score"`   // 融合排序分，只在本次结果内部可比
}

// knowledgeRetrieveResult 是工具出参。
type knowledgeRetrieveResult struct {
	Results []knowledgeRetrieveHit `json:"results"`

	// Error 非空表示这次检索**没有做成**（向量服务不可用、数据库故障……）。
	// 它与"检索成功、但库里确实没有相关内容"是两回事：后者 results 为空、Error 也为空。
	// 之所以不直接返回 Go error，是因为 Eino 的工具错误会中断整条 agent 运行 ——
	// 一次检索失败就把一整节课的生成打断，代价与它想表达的信息严重不成比例。
	// 交给模型判断：它可以改用别的工具，或直接凭已有知识作答。
	Error string `json:"error,omitempty"`
}

// knowledgeRetrieveTool 实现 Eino 的 tool.InvokableTool。
type knowledgeRetrieveTool struct {
	searcher KnowledgeSearcher
}

// NewKnowledgeRetrieveTool 构造内置的知识库检索工具。
func NewKnowledgeRetrieveTool(searcher KnowledgeSearcher) (tool.BaseTool, error) {
	if searcher == nil {
		return nil, fmt.Errorf("知识库检索工具需要非空的检索能力")
	}
	return &knowledgeRetrieveTool{searcher: searcher}, nil
}

// Info 返回工具元信息。Desc 是模型判断"什么时候该用它"的唯一依据，所以写清楚边界：
// 查用户自己的资料用这个，查公开信息用联网搜索。
func (t *knowledgeRetrieveTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: knowledgeRetrieveToolName,
		Desc: "检索本机知识库，取回与问题最相关的原文片段。" +
			"知识库里的内容都是用户自己上传或录入的（课件、讲义、笔记、内部文档），" +
			"凡是需要依据这些资料作答、或需要核对原文细节的问题都应当先调用它；" +
			"查公开的、时效性的信息请改用联网搜索工具。" +
			"返回的每条结果都带来源，引用时请说明出处。" +
			"若返回里带 error 字段，说明本次检索未成功，可改用其他工具或凭已有知识作答。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"query": {
				Type:     schema.String,
				Desc:     "检索词：一句自然语言、关键词，或两者混着写。用提问的原话即可，不要加引号或布尔语法。",
				Required: true,
			},
			"top_k": {
				Type: schema.Integer,
				Desc: "返回条数，默认 5、上限 50。问题较宽泛时可以多要几条（如 10）。",
			},
		}),
	}, nil
}

// InvokableRun 执行一次检索，返回契约形状的 JSON。
func (t *knowledgeRetrieveTool) InvokableRun(ctx context.Context, arguments string, _ ...tool.Option) (string, error) {
	var args knowledgeRetrieveArgs
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		// 参数不是合法 JSON 属于协议级错误（模型本该产出 JSON），
		// 与远端 MCP 工具的处理保持一致：直接报错，不替模型猜它想说什么。
		return "", fmt.Errorf("%s 的参数不是合法 JSON: %w", knowledgeRetrieveToolName, err)
	}

	if strings.TrimSpace(args.Query) == "" {
		return encodeKnowledgeRetrieve(knowledgeRetrieveResult{
			Results: []knowledgeRetrieveHit{},
			Error:   "query 不能为空：请把要查的问题或关键词填进 query",
		})
	}

	result, err := t.searcher.Retrieve(ctx, requestdto.KnowledgeRetrieve{Query: args.Query, TopK: args.TopK})
	if err != nil {
		// 失败详情留在日志里（排障要完整诊断），给模型的是同一句话的中文说明 ——
		// 模型不需要错误码，它需要的只是"这条路这次走不通"。
		logger.Warn("知识库检索工具调用失败",
			zap.String("query", args.Query),
			zap.Int("top_k", args.TopK),
			zap.Error(err),
		)
		return encodeKnowledgeRetrieve(knowledgeRetrieveResult{
			Results: []knowledgeRetrieveHit{},
			Error:   "检索失败：" + err.Error(),
		})
	}

	hits := make([]knowledgeRetrieveHit, 0, len(result.Results))
	for _, hit := range result.Results {
		hits = append(hits, knowledgeRetrieveHit{
			Content: hit.Content,
			Source:  hit.Source,
			Score:   hit.Score,
		})
	}
	return encodeKnowledgeRetrieve(knowledgeRetrieveResult{Results: hits})
}

// encodeKnowledgeRetrieve 把结果编成 JSON 字符串。
//
// 编码失败只可能是结构体里出现了不可编码的值（这里不可能），所以它不返回 error，
// 而是给一句明确的兜底：工具签名里多一个不可能触发的错误分支，只会让调用方纠结。
func encodeKnowledgeRetrieve(result knowledgeRetrieveResult) (string, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("编码 %s 结果失败: %w", knowledgeRetrieveToolName, err)
	}
	return string(encoded), nil
}
