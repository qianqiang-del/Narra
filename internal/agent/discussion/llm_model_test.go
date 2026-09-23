package discussion

import (
	"context"
	"errors"
	"strings"
	"testing"

	"narra/pkg/llm"
)

// fakeChatClient 记录请求、返回预置回复；不联网、不花钱。
type fakeChatClient struct {
	reply   string
	err     error
	request llm.ChatRequest
}

func (c *fakeChatClient) Chat(_ context.Context, req llm.ChatRequest) (*llm.Completion, error) {
	c.request = req
	if c.err != nil {
		return nil, c.err
	}
	return &llm.Completion{
		Content: c.reply,
		Usage:   &llm.Usage{PromptTokens: 11, CompletionTokens: 22},
	}, nil
}

func testGenerationRequest() GenerationRequest {
	return GenerationRequest{
		Participant: Participant{
			ClassroomAgentID: 1,
			Name:             "物理老师",
			Role:             "teacher",
			Persona:          "讲课时喜欢用生活里的例子",
		},
		Topic:  "为什么天空是蓝色的？",
		TurnNo: 2,
		History: []HistoryMessage{
			{Speaker: "好奇宝宝", Content: "是不是因为海水的颜色？"},
		},
	}
}

// 主路径：身份与任务提示词拼进 system、主题与历史拼进 user，回复按协议解析。
func TestLLMModelBuildsPromptAndParsesReply(t *testing.T) {
	client := &fakeChatClient{reply: `{"content":"因为瑞利散射，蓝光散射得更厉害。","next_action":"end"}`}
	model, err := NewLLMModel(client)
	if err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}

	response, err := model.Generate(context.Background(), testGenerationRequest())
	if err != nil {
		t.Fatalf("生成发言失败: %v", err)
	}

	if response.Content != "因为瑞利散射，蓝光散射得更厉害。" {
		t.Errorf("Content = %q", response.Content)
	}
	if response.NextAction != "end" {
		t.Errorf("NextAction = %q，期望 end", response.NextAction)
	}
	if response.InputTokens != 11 || response.OutputTokens != 22 {
		t.Errorf("token 用量 = %d/%d，期望 11/22（取自上游返回）",
			response.InputTokens, response.OutputTokens)
	}

	if len(client.request.Messages) != 2 {
		t.Fatalf("请求消息 = %d 条，期望 system + user 两条", len(client.request.Messages))
	}
	system := client.request.Messages[0]
	user := client.request.Messages[1]
	if system.Role != "system" || user.Role != "user" {
		t.Fatalf("消息角色 = %q / %q，期望 system / user", system.Role, user.Role)
	}

	// 身份层：名字、persona、圆桌任务协议都要在。
	for _, want := range []string{"物理老师", "讲课时喜欢用生活里的例子", "next_action"} {
		if !strings.Contains(system.Content, want) {
			t.Errorf("system 提示词里缺少 %q", want)
		}
	}
	// 输入层：主题、历史、轮次都要在。
	for _, want := range []string{"为什么天空是蓝色的", "好奇宝宝：是不是因为海水的颜色", "第 2 轮"} {
		if !strings.Contains(user.Content, want) {
			t.Errorf("user 提示词里缺少 %q", want)
		}
	}
}

// 模型没按 JSON 回话不算失败：整段当正文，动作留空交给 Director 兜底。
func TestLLMModelFallsBackWhenReplyIsNotJSON(t *testing.T) {
	client := &fakeChatClient{reply: "我觉得是因为海水的颜色。（没按协议回话）"}
	model, err := NewLLMModel(client)
	if err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}

	response, err := model.Generate(context.Background(), testGenerationRequest())
	if err != nil {
		t.Fatalf("坏格式不该让发言失败: %v", err)
	}
	if response.Content != "我觉得是因为海水的颜色。（没按协议回话）" {
		t.Errorf("Content = %q，期望整段原文", response.Content)
	}
	if response.NextAction != "" {
		t.Errorf("NextAction = %q，期望留空（由 Director 按换人兜底）", response.NextAction)
	}
}

// 模型爱给 JSON 套 ```json 围栏：协议里写了不要，但解析要能容住。
func TestLLMModelParsesCodeFencedReply(t *testing.T) {
	client := &fakeChatClient{reply: "```json\n{\"content\":\"好，换下一位。\",\"next_action\":\"switch_agent\"}\n```"}
	model, err := NewLLMModel(client)
	if err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}

	response, err := model.Generate(context.Background(), testGenerationRequest())
	if err != nil {
		t.Fatalf("生成发言失败: %v", err)
	}
	if response.Content != "好，换下一位。" || response.NextAction != "switch_agent" {
		t.Errorf("解析结果 = %q / %q", response.Content, response.NextAction)
	}
}

// 角色大类不在角色层里：拼不出提示词，宁可当场失败，也不拿空提示词去调模型。
func TestLLMModelRejectsUnknownRole(t *testing.T) {
	client := &fakeChatClient{reply: `{"content":"x"}`}
	model, err := NewLLMModel(client)
	if err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}

	request := testGenerationRequest()
	request.Participant.Role = "unknown"
	if _, err := model.Generate(context.Background(), request); err == nil {
		t.Fatal("未知角色大类应当直接报错")
	}
	if client.request.Messages != nil {
		t.Error("提示词都拼不出来，不该调用模型")
	}
}

// 模型什么都没回：这是真失败，让编排层收尾成 run.failed。
func TestLLMModelRejectsEmptyReply(t *testing.T) {
	client := &fakeChatClient{reply: "   \n  "}
	model, err := NewLLMModel(client)
	if err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}

	if _, err := model.Generate(context.Background(), testGenerationRequest()); err == nil {
		t.Fatal("空回复应当报错")
	}
}

// 上游调用失败要带上原因冒出去，编排层据此写 run.failed。
func TestLLMModelWrapsClientError(t *testing.T) {
	client := &fakeChatClient{err: errors.New("connection refused")}
	model, err := NewLLMModel(client)
	if err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}

	_, err = model.Generate(context.Background(), testGenerationRequest())
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("错误 = %v，期望带上上游原因", err)
	}
}

// 依赖缺失要在装配期暴露，而不是第一次发言时才炸。
func TestNewLLMModelRejectsNilClient(t *testing.T) {
	if _, err := NewLLMModel(nil); err == nil {
		t.Fatal("nil 客户端应当装配失败")
	}
}
