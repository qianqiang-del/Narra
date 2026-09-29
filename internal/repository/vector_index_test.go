package repository

import (
	"context"
	"errors"
	"testing"

	"narra/internal/model/entity"
)

// TestVectorIndexTypeFor 钉住索引精度的两段阈值：≤2000 单精度、2001–4000 半精度、
// 再往上建不出索引（哨兵错误）。2001 与 4001 是两条边界 —— 正是会撞上 pgvector
// "cannot have more than N dimensions" 的那两维。
func TestVectorIndexTypeFor(t *testing.T) {
	cases := []struct {
		name            string
		dimensions      int32
		want            vectorIndexType
		wantErr         bool
		wantUnsupported bool
	}{
		{"零维非法", 0, "", true, false},
		{"负维非法", -1, "", true, false},
		{"1 维单精度", 1, vectorIndexTypeVector, false, false},
		{"2000 维仍是单精度", 2000, vectorIndexTypeVector, false, false},
		{"2001 维转半精度", 2001, vectorIndexTypeHalfvec, false, false},
		{"4000 维仍是半精度", 4000, vectorIndexTypeHalfvec, false, false},
		{"4001 维建不出索引", 4001, "", true, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			model := &entity.EmbeddingModel{
				BaseModel:  entity.BaseModel{ID: 1},
				Name:       "阈值探针",
				Dimensions: testCase.dimensions,
			}
			got, err := vectorIndexTypeFor(model)
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("%d 维应当报错", testCase.dimensions)
				}
				if errors.Is(err, ErrVectorIndexUnsupported) != testCase.wantUnsupported {
					t.Fatalf("哨兵错误判定不对: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("%d 维不该报错: %v", testCase.dimensions, err)
			}
			if got != testCase.want {
				t.Fatalf("精度类型不对: got=%q want=%q", got, testCase.want)
			}
		})
	}
}

// TestVectorSearchCastType 校验检索侧的 cast 选择：必须与索引侧共用同一套阈值，
// 且数据库没有 halfvec 时高维退回全精度 vector —— 用不上索引但检索照常正确。
func TestVectorSearchCastType(t *testing.T) {
	cases := []struct {
		name             string
		dimensions       int
		halfvecAvailable bool
		want             vectorIndexType
	}{
		{"小维度恒用单精度", 1536, true, vectorIndexTypeVector},
		{"小维度在没有 halfvec 的环境也是单精度", 1536, false, vectorIndexTypeVector},
		{"2001 维有 halfvec 用半精度", 2001, true, vectorIndexTypeHalfvec},
		{"2001 维没有 halfvec 退回单精度", 2001, false, vectorIndexTypeVector},
		{"4000 维有 halfvec 用半精度", 4000, true, vectorIndexTypeHalfvec},
		{"超过 halfvec 上限退回单精度（本来就没有索引）", 4096, true, vectorIndexTypeVector},
		{"超过 halfvec 上限且没有 halfvec 也单精度", 4096, false, vectorIndexTypeVector},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := vectorSearchCastType(testCase.dimensions, testCase.halfvecAvailable); got != testCase.want {
				t.Fatalf("cast 类型不对: got=%q want=%q", got, testCase.want)
			}
		})
	}
}

// TestSearchRepositoryUseHalfvec 校验检索仓储的能力探测：只有维度落在
// (vector 上限, halfvec 上限] 才去问数据库有没有 halfvec，其余直接回答"不用"。
func TestSearchRepositoryUseHalfvec(t *testing.T) {
	db := openTestDB(t)
	skipWithoutHalfvec(t, db)

	repo := NewKnowledgeSearchRepository(db).(*knowledgeSearchRepository)
	if !repo.useHalfvec(context.Background(), maxVectorIndexDimensions+1) {
		t.Fatalf("%d 维应当用半精度 cast", maxVectorIndexDimensions+1)
	}
	if repo.useHalfvec(context.Background(), maxVectorIndexDimensions) {
		t.Fatalf("≤%d 维不该用半精度", maxVectorIndexDimensions)
	}
	if repo.useHalfvec(context.Background(), maxHalfvecIndexDimensions+1) {
		t.Fatalf("超过 %d 维没有索引可用，不该用半精度", maxHalfvecIndexDimensions)
	}
}
