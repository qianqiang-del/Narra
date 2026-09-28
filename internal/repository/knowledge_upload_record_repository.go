package repository

import (
	"context"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

// knowledgeUploadRecordRepository 基于 GORM 的上传记录仓储。
type knowledgeUploadRecordRepository struct {
	db *gorm.DB
}

// NewKnowledgeUploadRecordRepository 创建上传记录仓储。
func NewKnowledgeUploadRecordRepository(db *gorm.DB) KnowledgeUploadRecordRepository {
	return &knowledgeUploadRecordRepository{db: db}
}

// 与文档仓储一样，每个方法都通过 conn(ctx, r.db) 取句柄：它参与收录提交的
// 跨表事务（建文档 + 写 metadata + 建记录），见 knowledge_document_repository.go 开头的说明。

// CreateUploadRecord 插入一条上传记录。实体里只有 DocumentID 这个外键列、没有非空的
// 关联对象，所以不存在级联写入的副作用。
func (r *knowledgeUploadRecordRepository) CreateUploadRecord(ctx context.Context, record *entity.KnowledgeUploadRecord) error {
	return conn(ctx, r.db).Create(record).Error
}

// List 按创建时间倒序分页，并 LEFT JOIN 出关联文档的标题。
//
// id 也参与排序的理由与文档列表相同：同一批导入的 created_at 可能相同，
// 只按时间排会让翻页时出现重复或漏项。
//
// 计数与取页共用同一个 base（只差 JOIN 与 Select）：两者不同源的话，前端会收到
// "总数 3、本页 5 条"这种自相矛盾的响应。JOIN 是 LEFT 且 document_id 一对一，
// 不会让计数翻倍，但计数本身用不到标题，就留在 JOIN 之前。
func (r *knowledgeUploadRecordRepository) List(ctx context.Context, offset, limit int) ([]entity.KnowledgeUploadRecordView, int64, error) {
	base := conn(ctx, r.db).Model(&entity.KnowledgeUploadRecord{})

	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var rows []entity.KnowledgeUploadRecordView
	err := base.
		Select("knowledge_upload_records.*, knowledge_documents.title AS document_title, " +
			"knowledge_documents.ingest_stage AS document_ingest_stage, " +
			"knowledge_documents.metadata->>'stage' AS document_failed_stage").
		Joins("LEFT JOIN knowledge_documents ON knowledge_documents.id = knowledge_upload_records.document_id").
		Order("knowledge_upload_records.created_at DESC, knowledge_upload_records.id DESC").
		Offset(offset).Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// GetByID 按主键取一条记录，查不到时把 gorm.ErrRecordNotFound 原样交给调用方，
// 由服务层翻成"记录不存在"——与文档仓储同一种分工。
func (r *knowledgeUploadRecordRepository) GetByID(ctx context.Context, id uint64) (*entity.KnowledgeUploadRecord, error) {
	var record entity.KnowledgeUploadRecord
	if err := conn(ctx, r.db).Where("id = ?", id).First(&record).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

// DeleteRecord 在一个事务里删掉记录，并连带删掉它那个还没收录成功的文档。
//
// 三件事必须原子：读出记录、按条件删文档、删记录。分开做会出现"文档删了、记录还在，
// 或者反过来"的中间态，而删除本来就是用户点一下、期望一步到位的动作。
//
// 删文档的条件用 status <> 'ready' 表达"尚未收录成功"（pending / processing / failed），
// 与收录队列容量的判据互补：队列数的是 pending + processing，这里放宽到 failed ——
// 一份失败的上传同样是"没有成果"的投递，它的文档行留着只会占着列表。
func (r *knowledgeUploadRecordRepository) DeleteRecord(ctx context.Context, id uint64) error {
	return conn(ctx, r.db).Transaction(func(tx *gorm.DB) error {
		var record entity.KnowledgeUploadRecord
		if err := tx.Where("id = ?", id).First(&record).Error; err != nil {
			return err
		}

		if record.DocumentID != nil {
			if err := tx.Where("id = ? AND status <> ?", *record.DocumentID, entity.KnowledgeDocumentStatusReady).
				Delete(&entity.KnowledgeDocument{}).Error; err != nil {
				return err
			}
		}

		return tx.Where("id = ?", id).Delete(&entity.KnowledgeUploadRecord{}).Error
	})
}
