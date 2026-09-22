package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
)

// 这些用例不连数据库、不调向量服务：工具本身只是一层 JSON ↔ service 的适配，
// 检索怎么召回、怎么融合是 internal/rag 的事（那边有自己的用例）。

// fakeKnowledgeSearcher 记录最近一次收到的检索输入，返回预设结果或错误。
type fakeKnowledgeSearcher struct {
	result responsedto.KnowledgeRetrieveResult
	err    error

	input requestdto.KnowledgeRetrieve
	calls int
}

var _ KnowledgeSearcher = (*fakeKnowledgeSearcher)(nil)

func (f *fakeKnowledgeSearcher) Retrieve(ctx context.Context, input requestdto.KnowledgeRetrieve) (responsedto.KnowledgeRetrieveResult, error) {
	f.calls++
	f.input = input
	return f.result, f.err
}

func newTestKnowledgeTool(t *testing.T, searcher KnowledgeSearcher) tool.InvokableTool {
	t.Helper()

	built, err := NewKnowledgeRetrieveTool(searcher)
	if err != nil {
		t.Fatalf("构造工具失败: %v", err)
	}
	invokable, ok := built.(tool.InvokableTool)
	if !ok {
		t.Fatal("工具必须实现 InvokableTool，否则 agent 调不动它")
	}
	return invokable
}

// TestNewKnowledgeRetrieveToolRejectsNilSearcher 校验漏了依赖时在装配期就报错，
// 而不是等模型第一次调用才空指针。
func TestNewKnowledgeRetrieveToolRejectsNilSearcher(t *testing.T) {
	if _, err := NewKnowledgeRetrieveTool(nil); err == nil {
		t.Fatal("没有检索能力时应当报错")
	}
}

// TestKnowledgeRetrieveToolInfoDescribesContract 校验工具名与参数 schema。
//
// 名字是 agent 侧引用它的唯一标识（契约里写死了 rag_retrieve），参数 schema 是模型
// 唯一能看到的"怎么填" —— query 必须标成必填，否则模型会试着空着调用。
func TestKnowledgeRetrieveToolInfoDescribesContract(t *testing.T) {
	info, err := newTestKnowledgeTool(t, &fakeKnowledgeSearcher{}).Info(context.Background())
	if err != nil {
		t.Fatalf("读取工具信息失败: %v", err)
	}
	if info.Name != "rag_retrieve" {
		t.Fatalf("工具名必须是契约里的 rag_retrieve，实际 %q", info.Name)
	}
	if !strings.Contains(info.Desc, "知识库") || !strings.Contains(info.Desc, "联网搜索") {
		t.Fatalf("描述要写清「什么时候用它、什么时候改用别的工具」，实际是：%s", info.Desc)
	}

	// 用序列化后的 JSON 断言而不是直接读 schema 结构体：字段名与取值才是模型看到的东西，
	// 也免得把 eino-contrib/jsonschema 的字段类型钉进用例。
	params, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatalf("参数 schema 无效: %v", err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("参数 schema 不是合法 JSON: %v", err)
	}
	var schema struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("解析参数 schema 失败: %v", err)
	}

	// 顶层必须是 object：OpenAI 兼容接口拿它当函数的参数类型，缺了只能靠对方宽容。
	if schema.Type != "object" {
		t.Fatalf("参数 schema 的顶层类型应当是 object: %s", raw)
	}

	if schema.Properties["query"].Type != "string" {
		t.Fatalf("query 应当是字符串参数: %s", raw)
	}
	if schema.Properties["top_k"].Type != "integer" {
		t.Fatalf("top_k 应当是整数参数: %s", raw)
	}
	if !containsString(schema.Required, "query") {
		t.Fatalf("query 必须标成必填: %s", raw)
	}
	if containsString(schema.Required, "top_k") {
		t.Fatalf("top_k 应当可选（服务层会钳位）：%s", raw)
	}
}

// TestKnowledgeRetrieveToolPassesQueryAndTopK 校验入参原样透传给服务层。
//
// 工具不做 trim、不做默认值：检索词的归一化与 top_k 的钳位是服务层的口径
// （见 service.Retrieve 的注释），两层各做一份迟早会不一致。
func TestKnowledgeRetrieveToolPassesQueryAndTopK(t *testing.T) {
	searcher := &fakeKnowledgeSearcher{}
	output, err := newTestKnowledgeTool(t, searcher).InvokableRun(context.Background(), `{"query":"  向量 检索 ","top_k":3}`)
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if searcher.input.Query != "  向量 检索 " || searcher.input.TopK != 3 {
		t.Fatalf("入参没有原样透传: %+v", searcher.input)
	}
	if !strings.Contains(output, `"results"`) {
		t.Fatalf("输出里应当有 results: %s", output)
	}
}

// TestKnowledgeRetrieveToolReturnsOnlyContractFields 校验出参只带契约里的三个字段。
//
// chunk_id、document_id、相似度这些会被剔掉：工具结果原样进入下一轮上下文，
// 多一个字段就是每一轮都要付的 token，而且会诱导模型去引用它们。
func TestKnowledgeRetrieveToolReturnsOnlyContractFields(t *testing.T) {
	searcher := &fakeKnowledgeSearcher{result: responsedto.KnowledgeRetrieveResult{
		Model: "bge-m3",
		Terms: []string{"向量", "检索"},
		Results: []responsedto.KnowledgeHit{
			{
				ChunkID:    12,
				DocumentID: 3,
				Title:      "向量检索调研",
				ChunkIndex: 2,
				Heading:    "排序",
				Content:    "命中的原文切片",
				Source:     "向量检索调研.md",
				Score:      0.031,
				Method:     "hybrid",
			},
		},
	}}

	output, err := newTestKnowledgeTool(t, searcher).InvokableRun(context.Background(), `{"query":"向量检索"}`)
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}

	var decoded struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("输出不是合法 JSON: %v（%s）", err, output)
	}
	if len(decoded.Results) != 1 {
		t.Fatalf("命中条数不对: %s", output)
	}

	hit := decoded.Results[0]
	for name := range hit {
		switch name {
		case "content", "source", "score":
		default:
			t.Fatalf("出参里多带了 %q，工具结果会原样进上下文：%s", name, output)
		}
	}
	if hit["content"] != "命中的原文切片" || hit["source"] != "向量检索调研.md" {
		t.Fatalf("命中内容或来源不对: %s", output)
	}
}

// TestKnowledgeRetrieveToolReportsEmptyQueryAsResult 校验空检索词**不返回 Go error**。
//
// Eino 的工具错误会中断整条 agent 运行：为一个参数笔误把一整节课的生成打断，
// 代价与信息量严重不成比例。改成让结果里带 error 字段，模型自己会改。
func TestKnowledgeRetrieveToolReportsEmptyQueryAsResult(t *testing.T) {
	searcher := &fakeKnowledgeSearcher{}
	output, err := newTestKnowledgeTool(t, searcher).InvokableRun(context.Background(), `{"query":"   "}`)
	if err != nil {
		t.Fatalf("空检索词不该中断 agent 运行: %v", err)
	}
	if searcher.calls != 0 {
		t.Fatal("空检索词不该去调检索服务")
	}
	if !strings.Contains(output, `"error"`) || !strings.Contains(output, "query") {
		t.Fatalf("应当在结果里说明 query 不能为空: %s", output)
	}
	if !strings.Contains(output, `"results":[]`) {
		t.Fatalf("结果必须是空数组而不是 null: %s", output)
	}
}

// TestKnowledgeRetrieveToolReportsFailureAsResult 校验检索失败同样只体现在结果里，
// 且失败原因（中文说明）带给模型、完整诊断留给日志。
func TestKnowledgeRetrieveToolReportsFailureAsResult(t *testing.T) {
	searcher := &fakeKnowledgeSearcher{err: errors.New("检索失败: 向量服务未启用: 请先在设置页配置向量服务")}

	output, err := newTestKnowledgeTool(t, searcher).InvokableRun(context.Background(), `{"query":"向量检索"}`)
	if err != nil {
		t.Fatalf("检索失败不该中断 agent 运行: %v", err)
	}
	if !strings.Contains(output, "向量服务未启用") {
		t.Fatalf("失败原因要带给模型，它才能决定换工具还是凭已有知识作答：%s", output)
	}
}

// TestKnowledgeRetrieveToolRejectsInvalidJSON 校验参数不是合法 JSON 时直接报错 ——
// 这属于协议级错误（模型本该产出 JSON），不与"检索失败"混为一谈，
// 处理方式与远端 MCP 工具一致（见 adapter.go 的 einoTool.InvokableRun）。
func TestKnowledgeRetrieveToolRejectsInvalidJSON(t *testing.T) {
	searcher := &fakeKnowledgeSearcher{}
	if _, err := newTestKnowledgeTool(t, searcher).InvokableRun(context.Background(), `query=向量检索`); err == nil {
		t.Fatal("非法 JSON 应当报错")
	}
	if searcher.calls != 0 {
		t.Fatal("参数没解析出来就不该去调检索服务")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
