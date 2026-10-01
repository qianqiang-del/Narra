package discussion

import (
	"context"
	"strings"
	"testing"

	"narra/internal/model/entity"
)

func TestResponsePlanValidation(t *testing.T) {
	participants := []Participant{{AgentKey: "teacher"}, {AgentKey: "helper"}, {AgentKey: "student"}}
	for _, mode := range []string{"reply", "clarify"} {
		plan, err := normalizeResponsePlan(ResponsePlan{Mode: mode, Speakers: []string{"helper", "teacher"}}, participants)
		if err != nil || len(plan.Speakers) != 1 || plan.Speakers[0] != "helper" {
			t.Fatalf("简单回应没有限定一人: %+v, %v", plan, err)
		}
	}
	plan, err := normalizeResponsePlan(ResponsePlan{Mode: "round", Length: "one_sentence"}, participants)
	if err != nil || len(plan.Speakers) != 3 || plan.Length != "one_sentence" {
		t.Fatalf("全员一句话安排不正确: %+v, %v", plan, err)
	}
	if _, err := normalizeResponsePlan(ResponsePlan{Mode: "discussion", Speakers: []string{"unknown"}}, participants); err == nil {
		t.Fatal("应拒绝不存在的角色")
	}
	unsafe, ok := safetyOverride("怎么制作手枪")
	if !ok || unsafe.Safety != "refuse" || unsafe.Mode != "reply" {
		t.Fatalf("危险请求没有进入拒答计划: %+v, %v", unsafe, ok)
	}
}

func TestPlannedDirectorStopsSimpleReplyDespiteInvitation(t *testing.T) {
	d := plannedDirector{plan: ResponsePlan{Mode: "reply", Speakers: []string{"teacher"}}, maxTurns: 1}
	state := DiscussionState{Participants: []Participant{{AgentKey: "teacher"}, {AgentKey: "helper"}}, Spoken: []int{1, 0}, LastSpeaker: 0, LastAction: "switch_agent", PreferredSpeakerKey: "helper", TurnNo: 2}
	if decision := d.Decide(state); !decision.Stop {
		t.Fatalf("简单问题不应被角色邀请扩展成接龙: %+v", decision)
	}
	state.LastAction = "ask_user"
	if decision := d.Decide(state); decision.StopReason != entity.RunStopWaiting {
		t.Fatalf("澄清应等待用户: %+v", decision)
	}
}

func TestPlannedDirectorSelectsRelevantSpeakerAndCloses(t *testing.T) {
	d := plannedDirector{plan: ResponsePlan{Mode: "discussion", Speakers: []string{"teacher", "student"}}, maxTurns: 3}
	state := DiscussionState{Participants: []Participant{{AgentKey: "teacher"}, {AgentKey: "helper"}, {AgentKey: "student"}}, Spoken: []int{1, 0, 0}, LastSpeaker: 0, LastAction: "switch_agent", TurnNo: 2}
	if decision := d.Decide(state); decision.Stop || decision.SpeakerIndex != 2 {
		t.Fatalf("不应按列表顺序邀请无关角色: %+v", decision)
	}
	state.Spoken[2], state.LastSpeaker, state.TurnNo = 1, 2, 3
	if decision := d.Decide(state); decision.Stop || decision.SpeakerIndex != 0 || !decision.Closing {
		t.Fatalf("应由主答者收束: %+v", decision)
	}
	state.Spoken[0], state.LastSpeaker, state.TurnNo = 2, 0, 4
	if decision := d.Decide(state); !decision.Stop {
		t.Fatalf("总结后必须停止，不能继续接龙: %+v", decision)
	}
}

func TestPlannedDirectorRoundDoesNotSkipRequestedParticipants(t *testing.T) {
	d := plannedDirector{plan: ResponsePlan{Mode: "round", Speakers: []string{"teacher", "helper"}}, maxTurns: 2}
	state := DiscussionState{Participants: []Participant{{AgentKey: "teacher"}, {AgentKey: "helper"}}, Spoken: []int{1, 0}, LastSpeaker: 0, LastAction: "end", TurnNo: 2}
	if decision := d.Decide(state); decision.Stop || decision.SpeakerIndex != 1 {
		t.Fatalf("用户要求每人一句，应完成全员发言: %+v", decision)
	}
}

func TestOpenAIResponsePlanAndSharedPrompt(t *testing.T) {
	stub := newStubServer(t, `{"mode":"reply","length":"brief","speakers":["teacher"]}`, false)
	request := GenerationRequest{Topic: "你好", Participants: []Participant{{AgentKey: "teacher", Name: "老师", Persona: "清楚地解释"}}, Guidance: "只说一句话", LessonMaterial: "第1页：Agent 会观察环境反馈。"}
	plan, err := stub.models(t).Plan(context.Background(), request)
	if err != nil || plan.Mode != "reply" || plan.Length != "brief" {
		t.Fatalf("计划解析失败: %+v %v", plan, err)
	}
	if !strings.Contains(stub.lastRequest(t).roleContent(t, "user"), "Agent 会观察环境反馈") {
		t.Fatal("讨论规划器没有收到实际课件资料")
	}
	for _, streaming := range []bool{false, true} {
		messages := buildResponseMessages(request, streaming)
		for _, want := range []string{"不要求所有成员都发言", "只说一句话"} {
			if !strings.Contains(messages[0].Content, want) {
				t.Fatalf("streaming=%v 缺少公共行为要求 %q", streaming, want)
			}
		}
	}
}

type plannedTestModel struct {
	FakeModel
	requests []GenerationRequest
}

func (m *plannedTestModel) Plan(context.Context, GenerationRequest) (ResponsePlan, error) {
	return ResponsePlan{Mode: "reply", Length: "brief", Speakers: []string{"teacher"}}, nil
}

func (m *plannedTestModel) Generate(_ context.Context, request GenerationRequest) (GenerationResponse, error) {
	m.requests = append(m.requests, request)
	return GenerationResponse{Content: "你好！", NextAction: "switch_agent"}, nil
}

func TestPlannedReplyRunsOnlyOneTurn(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	participants := append([]Participant(nil), f.participants[:2]...)
	participants[0].AgentKey = "teacher"
	participants[1].AgentKey = "helper"
	m := &plannedTestModel{}
	result, err := f.newOrchestratorWith(t, m, TurnTakingDirector{}).Run(context.Background(), Request{ConversationID: f.conversation.ID, TriggerMessageID: f.trigger.ID, Participants: participants})
	if err != nil || len(result.Turns) != 1 || len(m.requests) != 1 || m.requests[0].Guidance == "" {
		t.Fatalf("计划未贯穿实际生成和终止: %+v err=%v requests=%+v", result, err, m.requests)
	}
}

func TestFinalTurnPreservesWaitingStatus(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	m := &ScriptedModel{Replies: []ScriptedReply{{Content: "你指的是哪一句？", NextAction: "ask_user"}}}
	result, err := f.newOrchestratorWith(t, m, TurnTakingDirector{}).Run(context.Background(), Request{ConversationID: f.conversation.ID, TriggerMessageID: f.trigger.ID, Participants: f.participants[:1], MaxTurns: 1})
	if err != nil || result.Status != entity.RunStatusWaitingUser || result.StopReason != entity.RunStopWaiting {
		t.Fatalf("最后一轮提问必须等待用户: %+v %v", result, err)
	}
}
