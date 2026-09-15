package service

import (
	"context"
	"strings"
	"testing"
)

func TestCatalogIntegrity(t *testing.T) {
	if len(voices) == 0 {
		t.Fatal("目录为空，测试前提不成立")
	}

	seen := make(map[string]bool)
	hasFemale, hasMale := false, false

	for _, v := range voices {
		if v.ID == "" || v.Name == "" || v.Gender == "" {
			t.Errorf("有字段为空的音色: %+v", v)
		}
		if v.Gender != VoiceGenderFemale && v.Gender != VoiceGenderMale {
			t.Errorf("音色 %q 的性别是 %q，不是已知取值", v.ID, v.Gender)
		}

		hasFemale = hasFemale || v.Gender == VoiceGenderFemale
		hasMale = hasMale || v.Gender == VoiceGenderMale

		if seen[v.ID] {
			t.Errorf("音色 ID %q 重复", v.ID)
		}
		seen[v.ID] = true
	}

	if !hasFemale || !hasMale {
		t.Errorf("目录缺了一种性别：女=%v 男=%v", hasFemale, hasMale)
	}
}

// List 的返回值会被控制器直接序列化成响应体，必须是副本。
func TestListReturnsCopy(t *testing.T) {
	svc := NewVoiceService(nil)

	first, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List 返回了错误: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("目录为空，测试前提不成立")
	}

	want := first[0].ID
	first[0].ID = "被改过的音色"

	second, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List 返回了错误: %v", err)
	}
	if second[0].ID != want {
		t.Errorf("没复制：ID = %q, want %q", second[0].ID, want)
	}
}

// 合成没启用时（tts 为 nil）试听要报错而不是 panic——服务没配 TTS_API_KEY 时就是这个状态。
func TestPreviewWithoutTTSReturnsError(t *testing.T) {
	svc := NewVoiceService(nil)

	if _, err := svc.Preview(context.Background(), voices[0].ID); err == nil {
		t.Fatal("合成未启用时 Preview 没有报错")
	}
}

// 音色不存在要在碰 TTS 之前就拦下：错误里得提到是哪个音色，而不是"合成未启用"。
func TestPreviewRejectsUnknownVoice(t *testing.T) {
	svc := NewVoiceService(nil)

	err := func() error {
		_, err := svc.Preview(context.Background(), "Stella")
		return err
	}()
	if err == nil {
		t.Fatal("未知音色没有被拦下")
	}
	if !strings.Contains(err.Error(), "Stella") {
		t.Errorf("校验顺序不对，碰 TTS 之前就该拦下: %v", err)
	}
}

func TestIsValidVoiceID(t *testing.T) {
	if len(voices) == 0 {
		t.Fatal("目录为空，测试前提不成立")
	}
	// 从目录里取一个真实存在的 ID，而不是写死一个——目录增删时这条测试不用跟着改。
	real := voices[0].ID

	testCases := []struct {
		name string
		id   string
		want bool
	}{
		{name: "目录里的音色", id: real, want: true},
		{name: "空字符串", id: "", want: false},
		{name: "展示名不是 ID", id: "芊悦", want: false},
		// 换厂商后旧的 voice_id 全部静默失效，只在这里被拦下。
		{name: "已删掉的音色", id: "Stella", want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsValidVoiceID(tc.id); got != tc.want {
				t.Errorf("IsValidVoiceID(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}
