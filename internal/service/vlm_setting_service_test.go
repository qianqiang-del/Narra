package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	requestdto "narra/internal/model/dto/request"
	"narra/internal/model/entity"
	appcrypto "narra/pkg/crypto"
	"narra/pkg/vlm"
)

// fakeVLMRepo 是内存版视觉模型配置仓储：行为对齐真实现（单条生效、缺失时不改状态），
// 让服务层用例完全离线。
type fakeVLMRepo struct {
	items  []entity.VLMSetting
	nextID uint64
}

func newFakeVLMRepo() *fakeVLMRepo { return &fakeVLMRepo{nextID: 1} }

func (f *fakeVLMRepo) List(context.Context) ([]entity.VLMSetting, error) {
	return append([]entity.VLMSetting{}, f.items...), nil
}

func (f *fakeVLMRepo) ListByOwner(_ context.Context, ownerID uint64) ([]entity.VLMSetting, error) {
	var out []entity.VLMSetting
	for _, item := range f.items {
		if item.OwnerID == ownerID {
			out = append(out, item)
		}
	}
	return out, nil
}

func (f *fakeVLMRepo) GetEnabledByOwner(_ context.Context, ownerID uint64) (*entity.VLMSetting, error) {
	for _, item := range f.items {
		if item.OwnerID == ownerID && item.IsEnabled {
			copy := item
			return &copy, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeVLMRepo) FindByIDAndOwner(ctx context.Context, id, ownerID uint64) (*entity.VLMSetting, error) {
	item, err := f.FindByID(ctx, id)
	if err != nil || item.OwnerID != ownerID {
		return nil, gorm.ErrRecordNotFound
	}
	return item, nil
}

func (f *fakeVLMRepo) DeleteByOwner(ctx context.Context, id, ownerID uint64) error {
	if _, err := f.FindByIDAndOwner(ctx, id, ownerID); err != nil {
		return err
	}
	return f.Delete(ctx, id)
}

func (f *fakeVLMRepo) SetEnabledForOwner(ctx context.Context, id, ownerID uint64, enabled bool) (bool, error) {
	if _, err := f.FindByIDAndOwner(ctx, id, ownerID); err != nil {
		return false, nil
	}
	if enabled {
		for i := range f.items {
			if f.items[i].OwnerID == ownerID {
				f.items[i].IsEnabled = false
			}
		}
	}
	for i := range f.items {
		if f.items[i].ID == id {
			f.items[i].IsEnabled = enabled
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeVLMRepo) GetEnabled(context.Context) (*entity.VLMSetting, error) {
	for index := range f.items {
		if f.items[index].IsEnabled {
			item := f.items[index]
			return &item, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeVLMRepo) FindByID(_ context.Context, id uint64) (*entity.VLMSetting, error) {
	for index := range f.items {
		if f.items[index].ID == id {
			item := f.items[index]
			return &item, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeVLMRepo) Create(_ context.Context, setting *entity.VLMSetting) error {
	setting.ID = f.nextID
	f.nextID++
	f.items = append(f.items, *setting)
	return nil
}

func (f *fakeVLMRepo) Update(_ context.Context, setting *entity.VLMSetting) error {
	for index := range f.items {
		if f.items[index].ID == setting.ID {
			f.items[index] = *setting
			return nil
		}
	}
	return gorm.ErrRecordNotFound
}

func (f *fakeVLMRepo) Delete(_ context.Context, id uint64) error {
	for index := range f.items {
		if f.items[index].ID == id {
			f.items = append(f.items[:index], f.items[index+1:]...)
			return nil
		}
	}
	return nil
}

func (f *fakeVLMRepo) SetEnabled(_ context.Context, id uint64, enabled bool) (bool, error) {
	target := -1
	for index := range f.items {
		if f.items[index].ID == id {
			target = index
			break
		}
	}
	if target < 0 {
		return false, nil
	}
	if enabled {
		for index := range f.items {
			f.items[index].IsEnabled = false
		}
	}
	f.items[target].IsEnabled = enabled
	return true, nil
}

// fakeVLMProber 是探测替身：返回预设结果，并可记录这次调用收到的连接配置。
type fakeVLMProber struct {
	result vlm.ProbeResult
	err    error
}

func (f *fakeVLMProber) Probe(context.Context) (vlm.ProbeResult, error) {
	return f.result, f.err
}

var vlmTestKey = []byte("0123456789abcdef0123456789abcdef")

// newVLMTestService 造一个注入了替身探测器的服务；返回替身仓储便于断言落库结果。
func newVLMTestService(t *testing.T) (VLMSettingService, *vlmSettingService, *fakeVLMRepo) {
	t.Helper()
	repo := newFakeVLMRepo()
	svc := NewVLMSettingService(repo, vlmTestKey).(*vlmSettingService)
	return svc, svc, repo
}

// seedVLMSetting 直接往替身仓储塞一条配置，模拟"库里的存量数据"。
func seedVLMSetting(t *testing.T, repo *fakeVLMRepo, status string, enabled bool) *entity.VLMSetting {
	t.Helper()
	encrypted, err := appcrypto.Encrypt("sk-seed", vlmTestKey)
	if err != nil {
		t.Fatalf("加密种子密钥失败: %v", err)
	}
	item := &entity.VLMSetting{
		Name: fmt.Sprintf("测试配置-%d", time.Now().UnixNano()), BaseURL: "https://api.siliconflow.cn/v1",
		Model: "Qwen/Qwen3.5-35B-A3B", TimeoutSeconds: 60, TestStatus: status,
		IsEnabled: enabled, APIKeyEncrypted: encrypted,
	}
	if err := repo.Create(context.Background(), item); err != nil {
		t.Fatalf("写入种子配置失败: %v", err)
	}
	return item
}

func TestVLMSettingsIsolateOwners(t *testing.T) {
	svc, _, repo := newVLMTestService(t)
	item := seedVLMSetting(t, repo, entity.VLMTestStatusSuccess, true)
	repo.items[0].OwnerID = 11
	ctx := context.Background()
	items, err := svc.List(ctx, 22)
	if err != nil || len(items) != 0 {
		t.Fatalf("other user's list: %v, %v", items, err)
	}
	if _, ok, err := svc.CurrentVision(ctx, 22); err != nil || ok {
		t.Fatalf("other user's VLM was visible: %v, %v", ok, err)
	}
	if _, err := svc.SetEnabled(ctx, item.ID, false, 22); err == nil {
		t.Fatal("other user changed setting")
	}
	if err := svc.Delete(ctx, item.ID, 22); err == nil {
		t.Fatal("other user deleted setting")
	}
	if _, ok, err := svc.CurrentVision(ctx, 11); err != nil || !ok {
		t.Fatalf("owner's VLM missing: %v, %v", ok, err)
	}
}

// 新建：字段归一化、Key 加密落库（不是明文）、默认未测试未启用。
func TestVLMCreateEncryptsKeyAndDefaults(t *testing.T) {
	svc, impl, repo := newVLMTestService(t)
	ctx := context.Background()

	dto, err := svc.Create(ctx, requestdto.VLMSetting{
		Name: " 硅基流动 · Qwen ", BaseURL: "https://api.siliconflow.cn/v1/",
		APIKey: "sk-test", Timeout: "60s", Model: " Qwen/Qwen3.5-35B-A3B ",
	})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if dto.Name != "硅基流动 · Qwen" || dto.BaseURL != "https://api.siliconflow.cn/v1" || dto.Model != "Qwen/Qwen3.5-35B-A3B" {
		t.Errorf("字段未归一化: %+v", dto)
	}
	if dto.Timeout != "1m0s" {
		t.Errorf("超时 = %q，期望 1m0s", dto.Timeout)
	}
	if !dto.APIKeyConfigured {
		t.Error("配置了 Key 时 api_key_configured 应为 true")
	}
	if dto.TestStatus != entity.VLMTestStatusUntested || dto.Enabled {
		t.Errorf("新建应为未测试且未启用: status=%q enabled=%v", dto.TestStatus, dto.Enabled)
	}

	stored := repo.items[0]
	if stored.APIKeyEncrypted == "" || stored.APIKeyEncrypted == "sk-test" {
		t.Errorf("Key 必须以密文落库，实际 %q", stored.APIKeyEncrypted)
	}
	decrypted, err := impl.decrypt(stored.APIKeyEncrypted)
	if err != nil || decrypted != "sk-test" {
		t.Errorf("解密回原文失败: %q err=%v", decrypted, err)
	}
}

// 输入校验：坏值一律 400 语义且不落库（边界与重排配置一致）。
func TestVLMCreateValidatesInput(t *testing.T) {
	cases := map[string]requestdto.VLMSetting{
		"空名称":     {BaseURL: "https://a.com/v1", Timeout: "60s", Model: "m"},
		"坏地址":     {Name: "n", BaseURL: "ftp://a.com", Timeout: "60s", Model: "m"},
		"短超时":     {Name: "n", BaseURL: "https://a.com/v1", Timeout: "200ms", Model: "m"},
		"超长超时":    {Name: "n", BaseURL: "https://a.com/v1", Timeout: "20m", Model: "m"},
		"空模型":     {Name: "n", BaseURL: "https://a.com/v1", Timeout: "60s"},
		"Key 带空格": {Name: "n", BaseURL: "https://a.com/v1", Timeout: "60s", Model: "m", APIKey: "sk x"},
		"Key 是地址": {Name: "n", BaseURL: "https://a.com/v1", Timeout: "60s", Model: "m", APIKey: "https://a.com/v1"},
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			svc, _, repo := newVLMTestService(t)
			if _, err := svc.Create(context.Background(), input); err == nil {
				t.Fatal("非法输入应当报错")
			}
			if len(repo.items) != 0 {
				t.Errorf("非法输入不应落库，实际 %d 条", len(repo.items))
			}
		})
	}
}

// 关键字段一变，测试状态与启用状态一起打回：旧配置测通过不代表新配置能用。
func TestVLMUpdateCriticalChangeResetsTestState(t *testing.T) {
	svc, _, repo := newVLMTestService(t)
	ctx := context.Background()
	item := seedVLMSetting(t, repo, entity.VLMTestStatusSuccess, true)

	dto, err := svc.Update(ctx, item.ID, requestdto.VLMSetting{
		Name: item.Name, BaseURL: item.BaseURL, Timeout: "60s", Model: "Qwen/Qwen3.5-9B",
	})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if dto.TestStatus != entity.VLMTestStatusUntested {
		t.Errorf("换模型后测试状态 = %q，期望 untested", dto.TestStatus)
	}
	if dto.Enabled {
		t.Error("关键字段变动后应停用")
	}
	if dto.LastTestError != nil {
		t.Errorf("关键字段变动后应清掉旧报错，实际 %v", *dto.LastTestError)
	}
}

// 只改名称（非关键字段）不动测试与启用状态。
func TestVLMUpdateNameOnlyKeepsTestState(t *testing.T) {
	svc, _, repo := newVLMTestService(t)
	ctx := context.Background()
	item := seedVLMSetting(t, repo, entity.VLMTestStatusSuccess, true)

	dto, err := svc.Update(ctx, item.ID, requestdto.VLMSetting{
		Name: "改了个名字", BaseURL: item.BaseURL, Timeout: "60s", Model: item.Model,
	})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if dto.TestStatus != entity.VLMTestStatusSuccess || !dto.Enabled {
		t.Errorf("仅改名称不应重置状态: status=%q enabled=%v", dto.TestStatus, dto.Enabled)
	}
}

// 清除 Key 也算关键变动：配置换了，测试结论作废。
func TestVLMUpdateClearKeyResets(t *testing.T) {
	svc, _, repo := newVLMTestService(t)
	ctx := context.Background()
	item := seedVLMSetting(t, repo, entity.VLMTestStatusSuccess, false)

	dto, err := svc.Update(ctx, item.ID, requestdto.VLMSetting{
		Name: item.Name, BaseURL: item.BaseURL, Timeout: "60s", Model: item.Model, ClearAPIKey: true,
	})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if dto.APIKeyConfigured {
		t.Error("清除后 api_key_configured 应为 false")
	}
	if dto.TestStatus != entity.VLMTestStatusUntested {
		t.Errorf("清除 Key 后测试状态 = %q，期望 untested", dto.TestStatus)
	}
}

// 测试成功：写回状态与模型回复，保持原启用状态，探测请求带上解密后的 Key。
func TestVLMTestSuccess(t *testing.T) {
	svc, impl, repo := newVLMTestService(t)
	ctx := context.Background()
	item := seedVLMSetting(t, repo, entity.VLMTestStatusUntested, true)

	var gotConfig vlm.Config
	impl.newProber = func(cfg vlm.Config) (vlmProber, error) {
		gotConfig = cfg
		return &fakeVLMProber{result: vlm.ProbeResult{Reply: "OK"}}, nil
	}

	result, err := svc.Test(ctx, item.ID)
	if err != nil {
		t.Fatalf("测试失败: %v", err)
	}
	if !result.Success || !strings.Contains(result.Message, "OK") {
		t.Errorf("测试结果应带上模型回复: %+v", result)
	}
	if gotConfig.BaseURL != item.BaseURL || gotConfig.Model != item.Model || gotConfig.Timeout != 60*time.Second {
		t.Errorf("探测配置不正确: %+v", gotConfig)
	}
	if gotConfig.APIKey != "sk-seed" {
		t.Errorf("探测应带解密后的 Key，实际 %q", gotConfig.APIKey)
	}
	reloaded, _ := repo.FindByID(ctx, item.ID)
	if reloaded.TestStatus != entity.VLMTestStatusSuccess || reloaded.LastTestError != nil {
		t.Errorf("成功结果未写回: %+v", reloaded)
	}
	if !reloaded.IsEnabled {
		t.Error("测试成功不应改变启用状态")
	}
}

// 测试失败：写回失败原因并停用（测不通的配置不该继续被解析使用）。
func TestVLMTestFailure(t *testing.T) {
	svc, impl, repo := newVLMTestService(t)
	ctx := context.Background()
	item := seedVLMSetting(t, repo, entity.VLMTestStatusSuccess, true)

	impl.newProber = func(vlm.Config) (vlmProber, error) {
		return &fakeVLMProber{err: fmt.Errorf("连接失败：connection refused")}, nil
	}

	if _, err := svc.Test(ctx, item.ID); err == nil {
		t.Fatal("探测失败时应当报错")
	}
	reloaded, _ := repo.FindByID(ctx, item.ID)
	if reloaded.TestStatus != entity.VLMTestStatusFailed {
		t.Errorf("失败后状态 = %q，期望 failed", reloaded.TestStatus)
	}
	if reloaded.LastTestError == nil || !strings.Contains(*reloaded.LastTestError, "connection refused") {
		t.Errorf("失败原因未写回: %v", reloaded.LastTestError)
	}
	if reloaded.IsEnabled {
		t.Error("测试失败应停用配置")
	}
}

// 启用前置：未测试通过的配置不许启用；存在的配置启用成功；不存在的返回 404 语义。
func TestVLMSetEnabled(t *testing.T) {
	svc, _, repo := newVLMTestService(t)
	ctx := context.Background()

	untested := seedVLMSetting(t, repo, entity.VLMTestStatusUntested, false)
	if _, err := svc.SetEnabled(ctx, untested.ID, true); err == nil {
		t.Fatal("未测试通过的配置不应允许启用")
	}
	if reloaded, _ := repo.FindByID(ctx, untested.ID); reloaded.IsEnabled {
		t.Error("被拒绝的启用不应改变库中状态")
	}

	ready := seedVLMSetting(t, repo, entity.VLMTestStatusSuccess, false)
	dto, err := svc.SetEnabled(ctx, ready.ID, true)
	if err != nil {
		t.Fatalf("启用失败: %v", err)
	}
	if !dto.Enabled {
		t.Error("启用后响应应为 enabled=true")
	}
	if enabled, err := repo.GetEnabled(ctx); err != nil || enabled.ID != ready.ID {
		t.Errorf("库中启用配置 = %v，期望 %d（err=%v）", enabled, ready.ID, err)
	}

	if _, err := svc.SetEnabled(ctx, 999999, true); err == nil {
		t.Error("不存在的配置应当报错")
	}
}

// 删除：不存在的配置报 404 语义；存在的删掉后查不到。
func TestVLMDelete(t *testing.T) {
	svc, _, repo := newVLMTestService(t)
	ctx := context.Background()

	if err := svc.Delete(ctx, 999999); err == nil {
		t.Error("删除不存在的配置应当报错")
	}

	item := seedVLMSetting(t, repo, entity.VLMTestStatusSuccess, false)
	if err := svc.Delete(ctx, item.ID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if len(repo.items) != 0 {
		t.Errorf("删除后不应残留，实际 %d 条", len(repo.items))
	}
}

// 测试连接（不落库）：用表单值探测成功；编辑时 Key 留空沿用已保存的那把；
// 坏输入 400 且不发请求；探测失败只报原因，不写任何状态。
func TestVLMTestConnection(t *testing.T) {
	svc, impl, repo := newVLMTestService(t)
	ctx := context.Background()

	var gotConfig vlm.Config
	calls := 0
	impl.newProber = func(cfg vlm.Config) (vlmProber, error) {
		calls++
		gotConfig = cfg
		return &fakeVLMProber{result: vlm.ProbeResult{Reply: "OK"}}, nil
	}

	// 1) 新配置：直接用输入值（含明文 Key），不落库。
	result, err := svc.TestConnection(ctx, requestdto.VLMSettingProbe{
		BaseURL: "https://api.siliconflow.cn/v1/", APIKey: "sk-draft",
		Timeout: "120s", Model: "Qwen/Qwen3.5-35B-A3B",
	})
	if err != nil {
		t.Fatalf("测试连接失败: %v", err)
	}
	if !result.Success || !strings.Contains(result.Message, "OK") {
		t.Errorf("测试结果不正确: %+v", result)
	}
	if gotConfig.BaseURL != "https://api.siliconflow.cn/v1" || gotConfig.APIKey != "sk-draft" ||
		gotConfig.Timeout != 120*time.Second {
		t.Errorf("探测配置不正确: %+v", gotConfig)
	}
	if len(repo.items) != 0 {
		t.Errorf("测试连接不应落库，实际 %d 条", len(repo.items))
	}

	// 2) 编辑既有配置且 Key 留空：沿用库里那把。
	item := seedVLMSetting(t, repo, entity.VLMTestStatusUntested, false)
	if _, err := svc.TestConnection(ctx, requestdto.VLMSettingProbe{
		ID: item.ID, BaseURL: item.BaseURL, Timeout: "120s", Model: item.Model,
	}); err != nil {
		t.Fatalf("带 ID 的测试连接失败: %v", err)
	}
	if gotConfig.APIKey != "sk-seed" {
		t.Errorf("Key 留空时应沿用已保存的密钥，实际 %q", gotConfig.APIKey)
	}

	// 3) 坏输入：400 语义且不发请求。
	before := calls
	if _, err := svc.TestConnection(ctx, requestdto.VLMSettingProbe{
		BaseURL: "ftp://a.com", Timeout: "120s", Model: "m",
	}); err == nil {
		t.Fatal("坏地址应当报错")
	}
	if calls != before {
		t.Errorf("坏输入不应发起探测，实际调用 %d 次", calls-before)
	}

	// 4) 探测失败：只报原因；已保存配置的状态保持不变。
	impl.newProber = func(vlm.Config) (vlmProber, error) {
		return &fakeVLMProber{err: fmt.Errorf("boom")}, nil
	}
	if _, err := svc.TestConnection(ctx, requestdto.VLMSettingProbe{
		BaseURL: "https://a.com/v1", Timeout: "120s", Model: "m",
	}); err == nil {
		t.Fatal("探测失败应当报错")
	}
	reloaded, _ := repo.FindByID(ctx, item.ID)
	if reloaded.TestStatus != entity.VLMTestStatusUntested || reloaded.IsEnabled {
		t.Errorf("测试连接不应改动已保存配置的状态: %+v", reloaded)
	}
}

// CurrentVision 是收录链路要的运行时面：没有启用项是"关闭"而不是错误；
// 启用时返回解密后的配置；密钥损坏才报错（调用方据此降级并留日志）。
func TestVLMCurrentVision(t *testing.T) {
	svc, _, repo := newVLMTestService(t)
	ctx := context.Background()

	if _, ok, err := svc.CurrentVision(ctx); ok || err != nil {
		t.Errorf("没有启用配置时应返回 ok=false 且无错误: ok=%v err=%v", ok, err)
	}

	item := seedVLMSetting(t, repo, entity.VLMTestStatusSuccess, true)
	cfg, ok, err := svc.CurrentVision(ctx)
	if !ok || err != nil {
		t.Fatalf("启用后应返回配置: ok=%v err=%v", ok, err)
	}
	if cfg.APIBaseURL != item.BaseURL || cfg.Model != item.Model ||
		cfg.APIKey != "sk-seed" || cfg.Timeout != 60*time.Second {
		t.Errorf("运行时配置不正确: %+v", cfg)
	}

	item.APIKeyEncrypted = "not-a-valid-ciphertext"
	if err := repo.Update(ctx, item); err != nil {
		t.Fatalf("写坏配置失败: %v", err)
	}
	if _, _, err := svc.CurrentVision(ctx); err == nil {
		t.Error("密钥解不开时应当报错（调用方据此降级为纯 OCR）")
	}
}
