package repository

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

// 这些用例必须跑真库：重排配置的关键行为都由 PostgreSQL 保证 ——
// is_enabled 上的部分唯一索引（最多一条生效）、SetEnabled 事务的原子性、
// 以及"目标不存在也不误关旧配置"的回滚语义。没有 DSN 时整体跳过。
//
// 本地运行：
//
//	NARRA_TEST_DSN="host=localhost port=5432 user=postgres password=*** dbname=narra sslmode=disable" \
//	    go test ./internal/repository/ -run Rerank -v

// ensureRerankTable 在测试事务里把表建出来，结构只看实体 tag；
// DDL 随事务回滚，既不依赖开发库跑没跑过 AutoMigrate，也不留痕。
func ensureRerankTable(t *testing.T, tx *gorm.DB) {
	t.Helper()
	if err := tx.AutoMigrate(&entity.RerankSetting{}); err != nil {
		t.Fatalf("建 rerank_settings 表失败: %v", err)
	}
}

// rerankTestSetting 造一条满足全部约束的配置（名字全局唯一，避免和开发库撞车）。
func rerankTestSetting(name string) entity.RerankSetting {
	return entity.RerankSetting{
		Name:           name,
		BaseURL:        "https://api.siliconflow.cn/v1",
		Model:          "BAAI/bge-reranker-v2-m3",
		TimeoutSeconds: 2,
		TestStatus:     entity.RerankTestStatusSuccess,
	}
}

// countRerankEnabled 数当前有几条启用配置。任何时刻都必须 ≤ 1。
func countRerankEnabled(t *testing.T, tx *gorm.DB) int64 {
	t.Helper()
	var count int64
	if err := tx.Model(&entity.RerankSetting{}).Where("is_enabled = ?", true).Count(&count).Error; err != nil {
		t.Fatalf("统计启用配置失败: %v", err)
	}
	return count
}

// 增删改查一条龙：写进去什么、读出来什么；测试结果能回写；删除后确实查不到。
func TestRerankSettingCrud(t *testing.T) {
	tx := testTx(t)
	ensureRerankTable(t, tx)
	ctx := context.Background()
	repo := NewRerankSettingRepository(tx)

	setting := rerankTestSetting(uniqueName("rerank-crud"))
	if err := repo.Create(ctx, &setting); err != nil {
		t.Fatalf("创建配置失败: %v", err)
	}

	got, err := repo.FindByID(ctx, setting.ID)
	if err != nil {
		t.Fatalf("按 ID 查询失败: %v", err)
	}
	if got.Name != setting.Name || got.BaseURL != setting.BaseURL || got.Model != setting.Model {
		t.Errorf("读回的基础字段不一致: %+v", got)
	}
	if got.TimeoutSeconds != 2 {
		t.Errorf("超时秒数 = %d，期望 2", got.TimeoutSeconds)
	}
	if got.IsEnabled {
		t.Error("新建配置默认不应启用")
	}
	if got.APIKeyEncrypted != "" {
		t.Errorf("未配置密钥时应为空串，实际 %q", got.APIKeyEncrypted)
	}

	// 测试失败回写：状态与报错原文都要能落库。
	message := "连接失败：connection refused"
	got.TestStatus, got.LastTestError = entity.RerankTestStatusFailed, &message
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("更新配置失败: %v", err)
	}
	reloaded, err := repo.FindByID(ctx, setting.ID)
	if err != nil {
		t.Fatalf("更新后重读失败: %v", err)
	}
	if reloaded.TestStatus != entity.RerankTestStatusFailed || reloaded.LastTestError == nil || *reloaded.LastTestError != message {
		t.Errorf("测试结果未正确回写: status=%q error=%v", reloaded.TestStatus, reloaded.LastTestError)
	}

	if err := repo.Delete(ctx, setting.ID); err != nil {
		t.Fatalf("删除配置失败: %v", err)
	}
	if _, err := repo.FindByID(ctx, setting.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("删除后应查不到，实际 err=%v", err)
	}
}

// 启用是排他的：启用 B 会取消 A，任何时刻最多一条 true（部分唯一索引兜底）。
func TestRerankSetEnabledKeepsSingleEnabled(t *testing.T) {
	tx := testTx(t)
	ensureRerankTable(t, tx)
	ctx := context.Background()
	repo := NewRerankSettingRepository(tx)

	first := rerankTestSetting(uniqueName("rerank-first"))
	second := rerankTestSetting(uniqueName("rerank-second"))
	for _, item := range []*entity.RerankSetting{&first, &second} {
		if err := repo.Create(ctx, item); err != nil {
			t.Fatalf("创建配置失败: %v", err)
		}
	}

	if applied, err := repo.SetEnabled(ctx, first.ID, true); err != nil || !applied {
		t.Fatalf("启用第一条失败: applied=%v err=%v", applied, err)
	}
	enabled, err := repo.GetEnabled(ctx)
	if err != nil {
		t.Fatalf("读取启用配置失败: %v", err)
	}
	if enabled.ID != first.ID {
		t.Fatalf("启用中的配置 = %d，期望 %d", enabled.ID, first.ID)
	}

	if applied, err := repo.SetEnabled(ctx, second.ID, true); err != nil || !applied {
		t.Fatalf("启用第二条失败: applied=%v err=%v", applied, err)
	}
	enabled, err = repo.GetEnabled(ctx)
	if err != nil {
		t.Fatalf("读取启用配置失败: %v", err)
	}
	if enabled.ID != second.ID {
		t.Fatalf("启用中的配置 = %d，期望 %d（启用第二条应自动取消第一条）", enabled.ID, second.ID)
	}
	if stale, err := repo.FindByID(ctx, first.ID); err != nil || stale.IsEnabled {
		t.Errorf("第一条应已被取消启用: enabled=%v err=%v", stale != nil && stale.IsEnabled, err)
	}
	if count := countRerankEnabled(t, tx); count != 1 {
		t.Errorf("启用配置数 = %d，期望 1", count)
	}
}

// 目标不存在时不能"启用失败、顺手把原来生效的也关了"：整个事务必须回滚。
func TestRerankSetEnabledRollsBackWhenTargetMissing(t *testing.T) {
	tx := testTx(t)
	ensureRerankTable(t, tx)
	ctx := context.Background()
	repo := NewRerankSettingRepository(tx)

	setting := rerankTestSetting(uniqueName("rerank-rollback"))
	if err := repo.Create(ctx, &setting); err != nil {
		t.Fatalf("创建配置失败: %v", err)
	}
	if applied, err := repo.SetEnabled(ctx, setting.ID, true); err != nil || !applied {
		t.Fatalf("启用失败: applied=%v err=%v", applied, err)
	}

	missingID := uint64(1) << 62
	applied, err := repo.SetEnabled(ctx, missingID, true)
	if err != nil {
		t.Fatalf("目标不存在不应报错，实际 %v", err)
	}
	if applied {
		t.Error("目标不存在时 applied 应为 false")
	}
	enabled, err := repo.GetEnabled(ctx)
	if err != nil {
		t.Fatalf("回滚后应仍有启用配置: %v", err)
	}
	if enabled.ID != setting.ID {
		t.Errorf("回滚后启用中的配置 = %d，期望仍是 %d", enabled.ID, setting.ID)
	}

	// 停用不存在的配置同样只是 applied=false，不影响现有状态。
	if applied, err := repo.SetEnabled(ctx, missingID, false); err != nil || applied {
		t.Errorf("停用不存在的配置: applied=%v err=%v（期望 false, nil）", applied, err)
	}
}

// 停用启用中的配置 → 精排回到"无生效配置"；删除启用中的配置同理。
func TestRerankDisableAndDeleteClearsEnabled(t *testing.T) {
	tx := testTx(t)
	ensureRerankTable(t, tx)
	ctx := context.Background()
	repo := NewRerankSettingRepository(tx)

	setting := rerankTestSetting(uniqueName("rerank-clear"))
	if err := repo.Create(ctx, &setting); err != nil {
		t.Fatalf("创建配置失败: %v", err)
	}
	if applied, err := repo.SetEnabled(ctx, setting.ID, true); err != nil || !applied {
		t.Fatalf("启用失败: applied=%v err=%v", applied, err)
	}

	if applied, err := repo.SetEnabled(ctx, setting.ID, false); err != nil || !applied {
		t.Fatalf("停用失败: applied=%v err=%v", applied, err)
	}
	if _, err := repo.GetEnabled(ctx); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("停用后应无生效配置，实际 err=%v", err)
	}

	if applied, err := repo.SetEnabled(ctx, setting.ID, true); err != nil || !applied {
		t.Fatalf("再次启用失败: applied=%v err=%v", applied, err)
	}
	if err := repo.Delete(ctx, setting.ID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := repo.GetEnabled(ctx); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("删除启用中的配置后应无生效配置，实际 err=%v", err)
	}
}

// 列表顺序：启用中的排最前，其余按 id 升序（开发库里已有的历史配置不影响这个相对顺序）。
func TestRerankSettingListOrdersEnabledFirst(t *testing.T) {
	tx := testTx(t)
	ensureRerankTable(t, tx)
	ctx := context.Background()
	repo := NewRerankSettingRepository(tx)

	older := rerankTestSetting(uniqueName("rerank-older"))
	newer := rerankTestSetting(uniqueName("rerank-newer"))
	for _, item := range []*entity.RerankSetting{&older, &newer} {
		if err := repo.Create(ctx, item); err != nil {
			t.Fatalf("创建配置失败: %v", err)
		}
	}
	if applied, err := repo.SetEnabled(ctx, newer.ID, true); err != nil || !applied {
		t.Fatalf("启用失败: applied=%v err=%v", applied, err)
	}

	items, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("列表查询失败: %v", err)
	}
	olderIndex, newerIndex := -1, -1
	for index, item := range items {
		switch item.ID {
		case older.ID:
			olderIndex = index
		case newer.ID:
			newerIndex = index
		}
	}
	if olderIndex < 0 || newerIndex < 0 {
		t.Fatalf("列表中应包含刚创建的两条配置: older=%d newer=%d", olderIndex, newerIndex)
	}
	if newerIndex > olderIndex {
		t.Errorf("启用中的配置应排在前面：newer=%d older=%d", newerIndex, olderIndex)
	}
}
