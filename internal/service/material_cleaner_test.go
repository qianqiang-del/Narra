package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

// 这组用例钉住到期材料清理的取舍：只删"确认仍到期且未关联"的材料，
// 名单里被删掉/被关联/被推迟的行一律安静跳过。

type fakeExpiredStore struct {
	ids       []uint64
	listErr   error
	documents map[uint64]*entity.KnowledgeDocument
}

func (s *fakeExpiredStore) ListExpiredMaterials(_ context.Context, _ time.Time) ([]uint64, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.ids, nil
}

func (s *fakeExpiredStore) GetByID(_ context.Context, id uint64) (*entity.KnowledgeDocument, error) {
	document, ok := s.documents[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return document, nil
}

type fakeMaterialDeleter struct {
	deleted []uint64
	err     error
}

func (d *fakeMaterialDeleter) Delete(_ context.Context, id uint64) error {
	if d.err != nil {
		return d.err
	}
	d.deleted = append(d.deleted, id)
	return nil
}

func expiredMaterial(expiresAt time.Time) *entity.KnowledgeDocument {
	return &entity.KnowledgeDocument{
		Kind:      entity.KnowledgeDocumentKindMaterial,
		ExpiresAt: &expiresAt,
	}
}

func newTestCleaner(t *testing.T, store expiredMaterialStore, deleter materialDeleter) *ExpiredMaterialCleaner {
	t.Helper()
	cleaner, err := NewExpiredMaterialCleaner(store, deleter)
	if err != nil {
		t.Fatalf("构造清理器失败: %v", err)
	}
	return cleaner
}

func TestNewExpiredMaterialCleanerRequiresDependencies(t *testing.T) {
	if _, err := NewExpiredMaterialCleaner(nil, nil); err == nil {
		t.Fatal("缺依赖时构造应当报错")
	}
}

func TestExpiredMaterialCleanerDeletesExpiredOnly(t *testing.T) {
	before := time.Now()
	expired := before.Add(-time.Hour)
	store := &fakeExpiredStore{
		ids: []uint64{7, 8, 9, 10},
		documents: map[uint64]*entity.KnowledgeDocument{
			7: expiredMaterial(expired),
			// 已被建课关联：expires_at 被清空，绝不能删。
			8: {Kind: entity.KnowledgeDocumentKindMaterial},
			// 知识库文档（不变式上不该带 expires_at），跳过。
			9: {Kind: entity.KnowledgeDocumentKindKnowledge, ExpiresAt: &expired},
			// 清理时间被推迟到 before 之后，这一轮不删。
			10: expiredMaterial(before.Add(time.Hour)),
		},
	}
	deleter := &fakeMaterialDeleter{}

	deleted, err := newTestCleaner(t, store, deleter).DeleteExpired(context.Background(), before)
	if err != nil {
		t.Fatalf("清理不该报错: %v", err)
	}
	if deleted != 1 || len(deleter.deleted) != 1 || deleter.deleted[0] != 7 {
		t.Fatalf("只该删除 7: deleted=%d calls=%v", deleted, deleter.deleted)
	}
}

func TestExpiredMaterialCleanerSkipsMissingRow(t *testing.T) {
	before := time.Now()
	store := &fakeExpiredStore{ids: []uint64{7}, documents: map[uint64]*entity.KnowledgeDocument{}}
	deleter := &fakeMaterialDeleter{}

	deleted, err := newTestCleaner(t, store, deleter).DeleteExpired(context.Background(), before)
	if err != nil || deleted != 0 || len(deleter.deleted) != 0 {
		t.Fatalf("已被删除的行应当安静跳过: deleted=%d calls=%v err=%v", deleted, deleter.deleted, err)
	}
}

func TestExpiredMaterialCleanerContinuesOnDeleteFailure(t *testing.T) {
	before := time.Now()
	store := &fakeExpiredStore{
		ids:       []uint64{7},
		documents: map[uint64]*entity.KnowledgeDocument{7: expiredMaterial(before.Add(-time.Hour))},
	}
	deleter := &fakeMaterialDeleter{err: errors.New("磁盘清理失败")}

	deleted, err := newTestCleaner(t, store, deleter).DeleteExpired(context.Background(), before)
	if err != nil {
		t.Fatalf("单篇失败不该让整轮失败: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("删除失败不该计入成功数: %d", deleted)
	}
}

func TestExpiredMaterialCleanerPropagatesListError(t *testing.T) {
	store := &fakeExpiredStore{listErr: errors.New("数据库挂了")}
	if _, err := newTestCleaner(t, store, &fakeMaterialDeleter{}).DeleteExpired(context.Background(), time.Now()); err == nil {
		t.Fatal("查询失败应当把错误交给 retention")
	}
}
