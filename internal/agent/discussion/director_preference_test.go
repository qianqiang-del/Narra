package discussion

import "testing"

func TestTurnTakingDirectorUsesModelPreferredSpeaker(t *testing.T) {
	participants := []Participant{
		{AgentKey: "teacher", Name: "老师"},
		{AgentKey: "curious", Name: "好奇宝宝"},
		{AgentKey: "clown", Name: "气氛组"},
	}
	decision := (TurnTakingDirector{}).Decide(DiscussionState{
		Participants:        participants,
		Spoken:              []int{1, 0, 0},
		LastSpeaker:         0,
		LastAction:          "switch_agent",
		PreferredSpeakerKey: "clown",
	})
	if decision.Stop || decision.SpeakerIndex != 2 {
		t.Fatalf("模型选中的角色没有生效，decision=%+v", decision)
	}
}
