package discussion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"narra/pkg/llm"
)

// 真实模型适配层的测试（第 8 步第 2 区块）。
//
// 用 httptest.Server 假扮 OpenAI 兼容接口、配一个真的 llm.Client：这一层要验证的是
// "讨论适配层发出的请求对不对、解析与报错语义对不对"。底层 HTTP 的细节
// （超时、非 200、响应不是 JSON）已由 pkg/llm 自己的用例覆盖，这里不重复。

// stubServer 假扮 /chat/completions，把收到的请求记下来，按预设内容作答。
type stubServer struct {
	server   *httptest.Server
	requests []capturedRequest
	// reply 是给模型回复的正文；sendUsage 决定响应里带不带 usage。
	reply     string
	sendUsage bool
	usage     llm.Usage
}

// capturedRequest 是服务端记下的一次请求。
type capturedRequest struct {
	path   string
	auth   string
	header http.Header
	body   chatStubBody
}

// chatStubBody 解出测试要断言的几个字段。
type chatStubBody struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Stream bool `json:"stream"`
}

// newStubServer 起一个假的对话补全服务。reply 是模型正文，sendUsage 控制是否返回用量。
func newStubServer(t *testing.T, reply string, sendUsage bool) *stubServer {
	t.Helper()

	stub := &stubServer{reply: reply, sendUsage: sendUsage, usage: llm.Usage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18}}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)

		var body chatStubBody
		_ = json.Unmarshal(raw, &body)
		stub.requests = append(stub.requests, capturedRequest{
			path:   r.URL.Path,
			auth:   r.Header.Get("Authorization"),
			header: r.Header.Clone(),
			body:   body,
		})

		response := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": stub.reply}, "finish_reason": "stop"},
			},
		}
		if stub.sendUsage {
			response["usage"] = stub.usage
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

// models 造一个连着假服务的适配器。
func (s *stubServer) models(t *testing.T) *OpenAIModels {
	t.Helper()

	client, err := llm.NewClient(llm.Config{BaseURL: s.server.URL, APIKey: "test-key", Model: "test-model"})
	if err != nil {
		t.Fatalf("创建大模型客户端失败: %v", err)
	}
	models, err := NewOpenAIModels(client)
	if err != nil {
		t.Fatalf("创建模型适配器失败: %v", err)
	}
	return models
}

// lastRequest 取服务端收到的最后一次请求。
func (s *stubServer) lastRequest(t *testing.T) capturedRequest {
	t.Helper()

	if len(s.requests) == 0 {
		t.Fatal("假服务一次请求都没收到")
	}
	return s.requests[len(s.requests)-1]
}

// roleContent 取指定角色的消息正文（本包只发 system 与 user 两条）。
func (r capturedRequest) roleContent(t *testing.T, role string) string {
	t.Helper()

	for _, message := range r.body.Messages {
		if message.Role == role {
			return message.Content
		}
	}
	t.Fatalf("请求里没有 %s 消息，实际有 %d 条", role, len(r.body.Messages))
	return ""
}

// testParticipant 是发言用例里的角色。
func testParticipant() Participant {
	return Participant{
		ClassroomAgentID: 1,
		Name:             "张老师",
		Role:             "teacher",
		Persona:          "讲得慢，爱举生活里的例子",
	}
}

// ---- 构造函数 ----

// TestNewOpenAIModelsRejectsNilClient 验证没有客户端就装配不起来。
func TestNewOpenAIModelsRejectsNilClient(t *testing.T) {
	models, err := NewOpenAIModels(nil)
	if err == nil {
		t.Fatal("客户端为 nil 时应当报错")
	}
	if models != nil {
		t.Error("报错时不该返回适配器")
	}
}

// ---- Generate ----

// TestOpenAIModelsGenerateSendsRequest 验证发言请求：地址、鉴权、模型、两条消息的内容。
func TestOpenAIModelsGenerateSendsRequest(t *testing.T) {
	stub := newStubServer(t, "```json\n{\"content\":\"  这是发言正文  \",\"next_action\":\"ask_user\"}\n```", true)
	models := stub.models(t)

	response, err := models.Generate(context.Background(), GenerationRequest{
		Participant: testParticipant(),
		Topic:       "为什么天空是蓝色的？",
		TurnNo:      2,
		History: []HistoryMessage{
			{Speaker: "好奇的小明", Content: "我觉得和散射有关。"},
			{Speaker: "严谨的小红", Content: "有依据吗？"},
		},
	})
	if err != nil {
		t.Fatalf("生成发言失败: %v", err)
	}

	request := stub.lastRequest(t)
	if request.path != "/chat/completions" {
		t.Errorf("请求路径 = %q，期望 /chat/completions", request.path)
	}
	if request.auth != "Bearer test-key" {
		t.Errorf("Authorization = %q，期望带上密钥", request.auth)
	}
	if request.body.Model != "test-model" {
		t.Errorf("请求的 model = %q，期望 test-model", request.body.Model)
	}
	if request.body.Stream {
		t.Error("发言不应使用流式请求")
	}
	if len(request.body.Messages) != 2 {
		t.Fatalf("消息条数 = %d，期望 2（system + user，不把历史拆成多条）", len(request.body.Messages))
	}
	if request.body.Messages[0].Role != "system" || request.body.Messages[1].Role != "user" {
		t.Errorf("消息角色 = %q / %q，期望 system / user",
			request.body.Messages[0].Role, request.body.Messages[1].Role)
	}

	system := request.roleContent(t, "system")
	for _, want := range []string{
		"张老师", "teacher", "讲得慢，爱举生活里的例子", "JSON", "next_action", "圆桌讨论",
		"continue：由当前发言人继续补充",
		"switch_agent：切换到另一位角色发言",
	} {
		if !strings.Contains(system, want) {
			t.Errorf("system 提示词里缺少 %q\n实际内容:\n%s", want, system)
		}
	}

	user := request.roleContent(t, "user")
	for _, want := range []string{"为什么天空是蓝色的？", "第 2 轮", "好奇的小明", "我觉得和散射有关。", "严谨的小红", "有依据吗？"} {
		if !strings.Contains(user, want) {
			t.Errorf("user 提示词里缺少 %q\n实际内容:\n%s", want, user)
		}
	}
	// 历史必须按原顺序出现，否则模型看到的对话是乱的。
	if strings.Index(user, "好奇的小明") > strings.Index(user, "严谨的小红") {
		t.Errorf("历史发言顺序被打乱了\n实际内容:\n%s", user)
	}

	// 返回值：正文去掉首尾空白，动作原样带出，用量取上游报的数。
	if response.Content != "这是发言正文" {
		t.Errorf("Content = %q，期望去掉首尾空白后的正文", response.Content)
	}
	if response.NextAction != "ask_user" {
		t.Errorf("NextAction = %q，期望 ask_user", response.NextAction)
	}
	if response.InputTokens != 11 || response.OutputTokens != 7 {
		t.Errorf("token 用量 = %d / %d，期望用上游报的 11 / 7", response.InputTokens, response.OutputTokens)
	}
}

// TestOpenAIModelsGenerateWithoutHistoryAndUsage 验证两处兜底：
// 没有历史时给出明确说明；上游不报用量时按字符数估算。
func TestOpenAIModelsGenerateWithoutHistoryAndUsage(t *testing.T) {
	stub := newStubServer(t, `{"content":"没有历史也要能说。","next_action":"continue"}`, false)
	models := stub.models(t)

	response, err := models.Generate(context.Background(), GenerationRequest{
		Participant: testParticipant(),
		Topic:       "第一次发言",
		TurnNo:      1,
	})
	if err != nil {
		t.Fatalf("生成发言失败: %v", err)
	}

	if user := stub.lastRequest(t).roleContent(t, "user"); !strings.Contains(user, "暂无历史发言") {
		t.Errorf("没有历史时 user 提示词里应明确写出来，实际内容:\n%s", user)
	}
	if response.NextAction != "continue" {
		t.Errorf("NextAction = %q，期望 continue", response.NextAction)
	}
	if response.InputTokens <= 0 || response.OutputTokens <= 0 {
		t.Errorf("上游没报用量时应按字符数估算，实际 = %d / %d", response.InputTokens, response.OutputTokens)
	}
	if want := estimateTokens("没有历史也要能说。"); response.OutputTokens != want {
		t.Errorf("OutputTokens = %d，期望按正文估算的 %d", response.OutputTokens, want)
	}
}

// TestOpenAIModelsGenerateRejectsBadReply 验证模型没按格式回话时报错，而不是把垃圾当发言。
func TestOpenAIModelsGenerateRejectsBadReply(t *testing.T) {
	cases := []struct {
		name  string
		reply string
	}{
		{name: "不是 JSON", reply: "我觉得天空是蓝色的，因为瑞利散射。🤔"},
		{name: "正文为空", reply: `{"content":"   ","next_action":"continue"}`},
		{name: "正文缺失", reply: `{"next_action":"continue"}`},
		{name: "空回复", reply: ""},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			stub := newStubServer(t, item.reply, false)
			models := stub.models(t)

			if _, err := models.Generate(context.Background(), GenerationRequest{
				Participant: testParticipant(), Topic: "主题", TurnNo: 1,
			}); err == nil {
				t.Fatalf("模型返回 %q 时应当报错", item.reply)
			} else if !strings.Contains(err.Error(), "生成讨论发言失败") {
				t.Errorf("错误信息 = %q，期望表明是生成发言失败", err.Error())
			}
		})
	}
}

// ---- Summarize ----

// TestOpenAIModelsSummarizeSendsRequest 验证摘要请求带上了上一版摘要与历史，返回值去掉空白。
func TestOpenAIModelsSummarizeSendsRequest(t *testing.T) {
	stub := newStubServer(t, "  此前聊到了瑞利散射，还没讲清波长与散射强度的关系。  ", false)
	models := stub.models(t)

	summary, err := models.Summarize(context.Background(),
		"上一版：确认了天空发蓝与散射有关。",
		[]HistoryMessage{
			{Speaker: "张老师", Content: "短波长更容易被散射。"},
			{Speaker: "好奇的小明", Content: "那为什么不是紫色的？"},
		},
	)
	if err != nil {
		t.Fatalf("生成摘要失败: %v", err)
	}

	if summary != "此前聊到了瑞利散射，还没讲清波长与散射强度的关系。" {
		t.Errorf("摘要 = %q，期望去掉首尾空白", summary)
	}

	request := stub.lastRequest(t)
	if len(request.body.Messages) != 2 {
		t.Fatalf("消息条数 = %d，期望 2（system + user）", len(request.body.Messages))
	}
	system := request.roleContent(t, "system")
	for _, want := range []string{"压缩", "不得编造"} {
		if !strings.Contains(system, want) {
			t.Errorf("system 提示词里缺少 %q\n实际内容:\n%s", want, system)
		}
	}
	user := request.roleContent(t, "user")
	for _, want := range []string{"上一版：确认了天空发蓝与散射有关。", "张老师", "短波长更容易被散射。", "好奇的小明", "那为什么不是紫色的？"} {
		if !strings.Contains(user, want) {
			t.Errorf("user 提示词里缺少 %q\n实际内容:\n%s", want, user)
		}
	}
}

// TestOpenAIModelsSummarizeRejectsEmpty 验证空摘要按错误处理（库里要求摘要比原文短且非空）。
func TestOpenAIModelsSummarizeRejectsEmpty(t *testing.T) {
	stub := newStubServer(t, "   ", false)
	models := stub.models(t)

	_, err := models.Summarize(context.Background(), "", []HistoryMessage{{Speaker: "张老师", Content: "说了点话"}})
	if err == nil {
		t.Fatal("摘要为空时应当报错")
	}
	if !strings.Contains(err.Error(), "生成上下文摘要失败") {
		t.Errorf("错误信息 = %q，期望表明是生成摘要失败", err.Error())
	}
}

// ---- Extract ----

// TestOpenAIModelsExtractParsesCandidates 验证提炼能解出候选，并按约定不再本地过滤。
func TestOpenAIModelsExtractParsesCandidates(t *testing.T) {
	reply := "```json\n" + `[
		{"memory_type":"fact","content":"天空发蓝与瑞利散射有关","importance":3},
		{"memory_type":"open_question","content":"为什么不是紫色的","importance":4}
	]` + "\n```"
	stub := newStubServer(t, reply, false)
	models := stub.models(t)

	candidates, err := models.Extract(context.Background(), []HistoryMessage{
		{Speaker: "张老师", Content: "短波长更容易被散射。"},
	})
	if err != nil {
		t.Fatalf("提炼记忆失败: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("候选条数 = %d，期望 2", len(candidates))
	}
	if candidates[0].MemoryType != "fact" || candidates[0].Content != "天空发蓝与瑞利散射有关" || candidates[0].Importance != 3 {
		t.Errorf("第 1 条候选 = %+v，字段没有原样带出", candidates[0])
	}
	if candidates[1].MemoryType != "open_question" || candidates[1].Importance != 4 {
		t.Errorf("第 2 条候选 = %+v，字段没有原样带出", candidates[1])
	}

	user := stub.lastRequest(t).roleContent(t, "user")
	if !strings.Contains(user, "短波长更容易被散射。") {
		t.Errorf("提炼材料里缺少讨论内容\n实际内容:\n%s", user)
	}
}

// TestOpenAIModelsExtractSkipsCallWithoutHistory 验证没有材料时不花这次调用。
func TestOpenAIModelsExtractSkipsCallWithoutHistory(t *testing.T) {
	stub := newStubServer(t, "[]", false)
	models := stub.models(t)

	candidates, err := models.Extract(context.Background(), nil)
	if err != nil {
		t.Fatalf("空历史不该报错: %v", err)
	}
	if candidates != nil {
		t.Errorf("空历史应返回 nil，实际 %+v", candidates)
	}
	if len(stub.requests) != 0 {
		t.Errorf("空历史不该发请求，实际发了 %d 次", len(stub.requests))
	}
}

// TestOpenAIModelsExtractRejectsBadReply 验证提炼结果不是 JSON 数组时报错。
func TestOpenAIModelsExtractRejectsBadReply(t *testing.T) {
	cases := []struct {
		name  string
		reply string
	}{
		{name: "不是 JSON", reply: "这场讨论没什么好记的。"},
		{name: "字段类型不对", reply: `{"memory_type":"fact"}`},
		{name: "空回复", reply: ""},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			stub := newStubServer(t, item.reply, false)
			models := stub.models(t)

			if _, err := models.Extract(context.Background(), []HistoryMessage{{Speaker: "张老师", Content: "说了点话"}}); err == nil {
				t.Fatalf("模型返回 %q 时应当报错", item.reply)
			} else if !strings.Contains(err.Error(), "提炼共享记忆失败") {
				t.Errorf("错误信息 = %q，期望表明是提炼记忆失败", err.Error())
			}
		})
	}
}

// ---- 上游失败 ----

// TestOpenAIModelsWrapsUpstreamError 验证上游报错时三项能力都带出各自的动作名。
func TestOpenAIModelsWrapsUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"rate limited"}}`)
	}))
	defer server.Close()

	client, err := llm.NewClient(llm.Config{BaseURL: server.URL, Model: "test-model"})
	if err != nil {
		t.Fatalf("创建大模型客户端失败: %v", err)
	}
	models, err := NewOpenAIModels(client)
	if err != nil {
		t.Fatalf("创建模型适配器失败: %v", err)
	}

	cases := []struct {
		name   string
		call   func() error
		reason string
	}{
		{
			name: "发言",
			call: func() error {
				_, err := models.Generate(context.Background(), GenerationRequest{Participant: testParticipant(), Topic: "主题", TurnNo: 1})
				return err
			},
			reason: "生成讨论发言失败",
		},
		{
			name: "摘要",
			call: func() error {
				_, err := models.Summarize(context.Background(), "", []HistoryMessage{{Speaker: "张老师", Content: "话"}})
				return err
			},
			reason: "生成上下文摘要失败",
		},
		{
			name: "提炼",
			call: func() error {
				_, err := models.Extract(context.Background(), []HistoryMessage{{Speaker: "张老师", Content: "话"}})
				return err
			},
			reason: "提炼共享记忆失败",
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			err := item.call()
			if err == nil {
				t.Fatal("上游报错时应当返回错误")
			}
			if !strings.Contains(err.Error(), item.reason) {
				t.Errorf("错误信息 = %q，期望表明是%s失败", err.Error(), item.name)
			}
			// 上游原因要留着，否则运维只知道"失败了"却不知道为什么。
			if !strings.Contains(err.Error(), "429") {
				t.Errorf("错误信息 = %q，期望保留上游的状态码", err.Error())
			}
		})
	}
}
