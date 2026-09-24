package rag

import "narra/internal/model/entity"

// RecoveryMaterial 是一次"恢复点计算"要看的现实材料。
//
// 收录阶段（ingest_stage）是**记录**，这三样才是**事实**。正常情况下两者由事务保证一致
// （阶段与它代表的数据一起写），但人工动过库、操作失误或将来加了编辑入口时可能漂移 ——
// 那时按事实回退，而不是按记录死守。
type RecoveryMaterial struct {
	HasOriginal bool // 原始文件还在服务器上
	HasContent  bool // 解析出的正文（documents.content）还在
	HasChunks   bool // 已落库的切片还在
}

// ResolveRecoveryStage 按现实材料算本次从哪一步恢复。
//
// 优先复用最靠后的产物，把重做量压到最小：
//
//	有切片 -> embed   （直接重新向量化，连切分都省了）
//	有正文 -> chunk   （重新切分，再向量化）
//	有原件 -> parse   （重新解析、切分、向量化）
//	都没有 -> ErrRecoveryInputMissing（只能请用户重新上传）
//
// 调用方有两处，且必须用同一个函数：重试入口（算好写回 ingest_stage）与 Worker
// 开始处理之前（防止"点了重试之后材料又没了"）。规则只有一份，两边才不会漂移。
func ResolveRecoveryStage(material RecoveryMaterial) (string, error) {
	switch {
	case material.HasChunks:
		return entity.KnowledgeDocumentStageEmbed, nil
	case material.HasContent:
		return entity.KnowledgeDocumentStageChunk, nil
	case material.HasOriginal:
		return entity.KnowledgeDocumentStageParse, nil
	default:
		return "", ErrRecoveryInputMissing
	}
}
