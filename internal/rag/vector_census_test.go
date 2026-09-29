package rag

import (
	"strings"
	"testing"

	"narra/internal/model/entity"
)

// TestVectorRecallHint 校验"静默零召回"的判定与边界：
// 只有"默认模型下 0 条、其他模型下有"才告警；正常的空结果与全新环境都不误报。
func TestVectorRecallHint(t *testing.T) {
	model := &entity.EmbeddingModel{BaseModel: entity.BaseModel{ID: 666}, Name: "新模型"}
	stale := entity.ModelVectorCount{ModelID: 6, Name: "旧模型", Vectors: 148}

	cases := []struct {
		name        string
		counts      []entity.ModelVectorCount
		wantContain string
	}{
		{
			name: "默认模型下有向量：零命中是正常空结果",
			counts: []entity.ModelVectorCount{
				{ModelID: 666, Name: "新模型", Vectors: 3}, stale,
			},
		},
		{
			name: "默认模型零条、旧模型有：要告警",
			counts: []entity.ModelVectorCount{
				{ModelID: 666, Name: "新模型", Vectors: 0}, stale,
			},
			wantContain: "默认模型 新模型（id=666）名下没有任何向量",
		},
		{
			name:   "全库都没有向量：首次部署，不告警",
			counts: []entity.ModelVectorCount{{ModelID: 666, Name: "新模型", Vectors: 0}},
		},
		{
			name:        "统计里没有默认模型这一行、但旧模型有：同样要告警",
			counts:      []entity.ModelVectorCount{stale},
			wantContain: "148 条",
		},
		{
			name:   "一个模型都没登记",
			counts: nil,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			hint := VectorRecallHint(model, testCase.counts)
			if testCase.wantContain == "" {
				if hint != "" {
					t.Fatalf("不该告警，实际: %s", hint)
				}
				return
			}
			// 告警必须给出"是什么、为什么、怎么办"：模型身份、其他模型的存量、修复动作。
			if !strings.Contains(hint, testCase.wantContain) || !strings.Contains(hint, "重新收录") {
				t.Fatalf("告警文案不完整: %s", hint)
			}
		})
	}

	if hint := VectorRecallHint(nil, nil); hint != "" {
		t.Fatalf("没有模型时不该告警: %s", hint)
	}
}
