package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

// knowledgeInsertBatch 是切片与向量的批量插入粒度。
//
// 一次上传可能有上千个切片，逐条 INSERT 会把往返次数放大到无法忍受；
// 而一个批次也不能太大 —— 向量的文本形式单个就有几十 KB，
// 批次过大会撞上 PostgreSQL 单条语句的参数上限（65535 个占位符）。
// 200 条 × 2 列远在安全线内，同时把往返次数压到可接受的范围。
const knowledgeInsertBatch = 200

// knowledgeDocumentRepository 基于 GORM 的知识库仓储。
type knowledgeDocumentRepository struct {
	db *gorm.DB
}

// SetMetadata 整份覆盖 metadata，不做合并（合并规则是调用方的事）。
func (r *knowledgeDocumentRepository) SetMetadata(ctx context.Context, id uint64, metadata json.RawMessage) error {
	return r.db.WithContext(ctx).Model(&entity.KnowledgeDocument{}).Where("id = ?", id).Update("metadata", metadata).Error
}

// ListPending 按创建时间正序取待处理文档（先进先出），只做查询、不改状态。
//
// 排序里带上 id 是因为同一批上传的 created_at 可能相同，只按时间排会让
// 前后两次取到的顺序不一致，任务可能被反复取到或被跳过。
func (r *knowledgeDocumentRepository) ListPending(ctx context.Context, limit int) ([]entity.KnowledgeDocument, error) {
	var documents []entity.KnowledgeDocument
	err := r.db.WithContext(ctx).Where("status = ?", entity.KnowledgeDocumentStatusPending).
		Order("created_at ASC, id ASC").Limit(limit).Find(&documents).Error
	return documents, err
}

// Claim 抢占一条待处理文档：带 status = 'pending' 条件的 UPDATE，抢到才返回 true。
//
// 条件写在 UPDATE 的 WHERE 里而不是"先查再改"，是为了让并发下的取舍由数据库
// 一次性完成 —— 两个执行者同时来，只有一个能让 RowsAffected 为 1。
//
// 用 Updates 传 map 而不是 UpdateColumn：claimed_at 这个语义要靠 updated_at 承担
// （ResetStale 按它判断僵尸任务），而 map 形式会触发 GORM 的 autoUpdateTime 自动带上它。
func (r *knowledgeDocumentRepository) Claim(ctx context.Context, id uint64) (bool, error) {
	result := r.db.WithContext(ctx).Model(&entity.KnowledgeDocument{}).
		Where("id = ? AND status = ?", id, entity.KnowledgeDocumentStatusPending).
		Updates(map[string]any{"status": entity.KnowledgeDocumentStatusProcessing})
	return result.RowsAffected == 1, result.Error
}

// ResetStale 把卡在 processing 超过时限的文档打回 pending，让它们重新入队。
//
// 判据用 updated_at 而不是新增一列"开始处理时间"：Claim 与 MarkProcessing 都是
// 走 GORM 的 map 更新，会自动刷新 updated_at，所以它天然就是"最后一次有人动过"的时刻。
// 代价是别处不能再用 UpdateColumn 之类的写法绕过 autoUpdateTime，否则这里会误判。
func (r *knowledgeDocumentRepository) ResetStale(ctx context.Context, olderThan time.Time) error {
	return r.db.WithContext(ctx).Model(&entity.KnowledgeDocument{}).
		Where("status = ? AND updated_at < ?", entity.KnowledgeDocumentStatusProcessing, olderThan).
		Updates(map[string]any{"status": entity.KnowledgeDocumentStatusPending}).Error
}

// Delete 硬删除一篇文档，切片与向量由外键 ON DELETE CASCADE 带走。
//
// 走硬删除（实体没有 DeletedAt）而不是软删除，原因和 ReplaceChunks 里删旧切片一样：
// 留着旧行会让 UNIQUE (document_id, chunk_index) 挡住同一篇文章的重新导入。
func (r *knowledgeDocumentRepository) Delete(ctx context.Context, id uint64) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&entity.KnowledgeDocument{}).Error
}

// NewKnowledgeDocumentRepository 创建知识库仓储。
func NewKnowledgeDocumentRepository(db *gorm.DB) KnowledgeDocumentRepository {
	return &knowledgeDocumentRepository{db: db}
}

// Create 插入一篇文档。实体里没有关联字段，所以不存在级联写入的副作用。
func (r *knowledgeDocumentRepository) Create(ctx context.Context, document *entity.KnowledgeDocument) error {
	return r.db.WithContext(ctx).Create(document).Error
}

// GetByID 按主键取一篇文档，查不到时把 gorm.ErrRecordNotFound 原样交给调用方，
// 由服务层翻成"文档不存在"。不在这里翻译成业务错误，是因为"记录不存在"在
// 收录链路（判断默认向量模型是否已登记）和 HTTP 面（404 语义）里的含义并不相同。
func (r *knowledgeDocumentRepository) GetByID(ctx context.Context, id uint64) (*entity.KnowledgeDocument, error) {
	var document entity.KnowledgeDocument
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&document).Error; err != nil {
		return nil, err
	}
	return &document, nil
}

// List 按创建时间倒序分页，条件来自 entity.KnowledgeDocumentQuery。
//
// id 也参与排序，因为同一批导入的文档 created_at 可能相同，只按时间排会让翻页时
// 出现重复或漏项。
//
// 计数与取页共用同一组 WHERE：两者不同源的话，前端会收到"总数 3、本页 5 条"这种
// 自相矛盾的响应，而它正是靠 total 算"已显示 X / Y"和判断还有没有下一页的。
func (r *knowledgeDocumentRepository) List(ctx context.Context, query entity.KnowledgeDocumentQuery) ([]entity.KnowledgeDocument, int64, error) {
	scope := r.db.WithContext(ctx).Model(&entity.KnowledgeDocument{}).Scopes(documentConditions(query))

	var total int64
	if err := scope.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var documents []entity.KnowledgeDocument
	err := scope.Order("created_at DESC, id DESC").Offset(query.Offset).Limit(query.Limit).Find(&documents).Error
	if err != nil {
		return nil, 0, err
	}
	return documents, total, nil
}

// CountActive 统计还在收录中的文档数（pending + processing）。
//
// 它同时覆盖两种"忙"：worker 正在处理的行（processing），以及刚提交、还没被
// worker 取走的行（pending）。判据用库里的行数而不是进程内的标志位，是因为活的
// 执行者是 rag.Worker —— 提交请求早就返回了，只有文档状态知道后台还在不在忙，
// 进程重启后这一点依然成立。
//
// 这个查询吃得到 knowledge_documents_status_created_at_idx 这条部分索引：
// 它的谓词正是 status IN ('pending', 'processing')。
func (r *knowledgeDocumentRepository) CountActive(ctx context.Context) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&entity.KnowledgeDocument{}).
		Where("status IN ?", []string{
			entity.KnowledgeDocumentStatusPending,
			entity.KnowledgeDocumentStatusProcessing,
		}).
		Count(&total).Error
	return total, err
}

// documentConditions 把查询条件翻成 WHERE 子句；条件为空时不加任何约束。
func documentConditions(query entity.KnowledgeDocumentQuery) func(*gorm.DB) *gorm.DB {
	return func(tx *gorm.DB) *gorm.DB {
		if len(query.Statuses) > 0 {
			tx = tx.Where("status IN ?", query.Statuses)
		}
		if keyword := strings.TrimSpace(query.Keyword); keyword != "" {
			pattern := likePattern(keyword)
			// 括号不能省：GORM 把多个 Where 用 AND 串起来，而 AND 比 OR 结合得紧，
			// 少了它这条会变成 "(status IN (…) AND title ILIKE …) OR source_uri ILIKE …"，
			// 后半句绕过了状态约束，会把别的状态的文档一起捞出来。
			tx = tx.Where("(title ILIKE ? OR source_uri ILIKE ?)", pattern, pattern)
		}
		return tx
	}
}

// likePattern 把用户输入包成 LIKE 模式，并转义模式里的通配符。
//
// 不转义的话，用户输入一个 % 就等于"匹配所有文档"，下划线同理 ——
// 界面上看起来像"搜索失效"，实际是把通配符的控制权交给了调用方。
// PostgreSQL 的 LIKE 默认转义字符就是反斜杠，所以这里补一个即可，不必写 ESCAPE 子句。
func likePattern(keyword string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(keyword)
	return "%" + escaped + "%"
}

// CountChunksByDocument 用一条 GROUP BY 查询批量取回切片数，避免列表页的 N+1。
func (r *knowledgeDocumentRepository) CountChunksByDocument(ctx context.Context, documentIDs []uint64) (map[uint64]int64, error) {
	counts := make(map[uint64]int64, len(documentIDs))
	if len(documentIDs) == 0 {
		return counts, nil
	}

	var rows []struct {
		DocumentID uint64
		Total      int64
	}
	err := r.db.WithContext(ctx).
		Model(&entity.KnowledgeChunk{}).
		Select("document_id, count(*) AS total").
		Where("document_id IN ?", documentIDs).
		Group("document_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		counts[row.DocumentID] = row.Total
	}
	return counts, nil
}

// MarkProcessing 推进到 processing。
//
// 用 Updates 传 map 而不是 UpdateColumn：map 形式的更新仍然会走 GORM 的
// autoUpdateTime 逻辑，updated_at 会被自动带上；UpdateColumn 会跳过它，
// 让这个字段永远停在创建时间（本项目的 updated_at 没有数据库触发器兜底）。
func (r *knowledgeDocumentRepository) MarkProcessing(ctx context.Context, id uint64) error {
	return r.db.WithContext(ctx).
		Model(&entity.KnowledgeDocument{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": entity.KnowledgeDocumentStatusProcessing}).Error
}

// MarkFailed 推进到 failed 并把失败现场写进 metadata。
func (r *knowledgeDocumentRepository) MarkFailed(ctx context.Context, id uint64, metadata json.RawMessage) error {
	updates := map[string]any{"status": entity.KnowledgeDocumentStatusFailed}
	if len(metadata) > 0 {
		updates["metadata"] = metadata
	}
	return r.db.WithContext(ctx).
		Model(&entity.KnowledgeDocument{}).
		Where("id = ?", id).
		Updates(updates).Error
}

// ReplaceChunks 在一个事务里替换切片与向量，并把文档置为 ready。
func (r *knowledgeDocumentRepository) ReplaceChunks(ctx context.Context, id uint64, input entity.ChunkReplacement) error {
	if len(input.Chunks) != len(input.Embeddings) {
		return fmt.Errorf("切片与向量数量不一致: %d 个切片 / %d 个向量", len(input.Chunks), len(input.Embeddings))
	}
	if len(input.Chunks) == 0 {
		// 一篇 ready 但没有任何切片的文档，在检索里等同于不存在，
		// 但它会占着列表、让用户以为已经入库成功。
		return fmt.Errorf("文档 %d 没有可写入的切片", id)
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 先删旧切片。删除走的是硬删除（实体没有 DeletedAt），
		//    这正是需要的效果：UNIQUE (document_id, chunk_index) 要求同序号的上一条
		//    必须先消失，软删除会让重复导入同一篇文章直接撞唯一约束。
		//    旧向量不需要显式删：knowledge_embeddings 的外键是 ON DELETE CASCADE。
		if err := tx.Where("document_id = ?", id).Delete(&entity.KnowledgeChunk{}).Error; err != nil {
			return err
		}

		// 2. 批量插入切片。必须插完才知道自增主键 —— 向量的 chunk_id 依赖它，
		//    所以这里不能用返回 ID 之外的方式省这一步。
		//    两个实体的关联字段（Chunk / Model / Document）都保持 nil，
		//    GORM 对 nil 的 belongs-to 关联不会做任何级联写入。
		if err := tx.CreateInBatches(input.Chunks, knowledgeInsertBatch).Error; err != nil {
			return err
		}
		for index := range input.Embeddings {
			input.Embeddings[index].ChunkID = input.Chunks[index].ID
		}
		if err := tx.CreateInBatches(input.Embeddings, knowledgeInsertBatch).Error; err != nil {
			return err
		}

		// 3. 最后更新文档本身。这一步必须在切片写好之后、且在同一个事务里：
		//    status = 'ready' 的 CHECK 约束要求 content 非空，而"内容已经落库"
		//    与"文档标记为可检索"如果分开提交，中间失败会留下一个 ready
		//    却没有切片的文档 —— 用户看到入库成功，检索却永远搜不到它。
		updates := map[string]any{
			"title":            input.Title,
			"content":          input.Content,
			"content_checksum": input.Checksum,
			"status":           entity.KnowledgeDocumentStatusReady,
		}
		if len(input.Metadata) > 0 {
			updates["metadata"] = input.Metadata
		}
		return tx.Model(&entity.KnowledgeDocument{}).Where("id = ?", id).Updates(updates).Error
	})
}
