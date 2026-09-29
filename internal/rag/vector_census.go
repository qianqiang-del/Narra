// vector_census.go 向量召回的体检：识别"默认模型下没有向量"的静默零召回，给出告警文案。
//
// 为什么需要：换过默认模型而没重新收录时，检索只在新模型名下查向量，旧向量挂在旧
// 模型名下，于是向量路一条都召回不到。这条路径**不报错** —— SQL 查询成功、结果为空，
// 表现为"知识库检索好像还行，但总觉得差一点"。文档里写过这个坑
// （docs/rag-database.md 的换模型警示），但运行时没有任何提示；这里把文档里的告警
// 搬到运行日志里，并让设置页保存/启动对齐（service）与检索链路（Retriever）共用
// 同一套判定 —— 两处各写一份的话，措辞与边界迟早分叉。
package rag

import (
	"fmt"
	"strings"

	"narra/internal/model/entity"
)

// VectorRecallHint 判断"默认模型下没有任何向量、其他模型下还有"的静默零召回状态，
// 返回一句告警文案；一切正常时返回空串。
//
// 返回空串的两种情况要分清：
//
//   - 默认模型下有向量 —— 零命中就是一次正常的空结果，别误报；
//   - 全库都没有向量 —— 首次部署的中间态，不是"换过模型"的形态，提示了也没人可修。
func VectorRecallHint(model *entity.EmbeddingModel, counts []entity.ModelVectorCount) string {
	if model == nil {
		return ""
	}

	var stale []string
	var others int64
	for _, count := range counts {
		if count.ModelID == model.ID {
			if count.Vectors > 0 {
				return ""
			}
			continue
		}
		if count.Vectors > 0 {
			others += count.Vectors
			stale = append(stale, fmt.Sprintf("%s（id=%d）%d 条", count.Name, count.ModelID, count.Vectors))
		}
	}
	if others == 0 {
		return ""
	}

	return fmt.Sprintf(
		"向量召回会静默为零：默认模型 %s（id=%d）名下没有任何向量，而其他模型下有 %d 条（%s）；"+
			"常见原因是换过默认模型却没有重新收录文档 —— 重新收录后向量路才会恢复",
		model.Name, model.ID, others, strings.Join(stale, "、"))
}
