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
	"narra/pkg/rerank"
)

// fakeRerankRepo 是内存版重排配置仓储：行为对齐真实现（单条生效、缺失时不改状态），
// 让服务层用例完全离线。
type fakeRerankRepo struct {
	items  []entity.RerankSetting
	nextID uint64
}

func newFakeRerankRepo() *fakeRerankRepo { return &fakeRerankRepo{nextID: 1} }

func (f *fakeRerankRepo) List(context.Context) ([]entity.RerankSetting, error) {
	return append([]entity.RerankSetting{}, f.items...), nil
}

func (f *fakeRerankRepo) GetEnabled(context.Context) (*entity.RerankSetting, error) {
	for index := range f.items {
		if f.items[index].IsEnabled {
			item := f.items[index]
			return &item, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeRerankRepo) FindByID(_ context.Context, id uint64) (*entity.RerankSetting, error) {
	for index := range f.items {
		if f.items[index].ID == id {
			item := f.items[index]
			return &item, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeRerankRepo) Create(_ context.Context, setting *entity.RerankSetting) error {
	setting.ID = f.nextID
	f.nextID++
	f.items = append(f.items, *setting)
	return nil
}

func (f *fakeRerankRepo) Update(_ context.Context, setting *entity.RerankSetting) error {
	for index := range f.items {
		if f.items[index].ID == setting.ID {
			f.items[index] = *setting
			return nil
		}
	}
	return gorm.ErrRecordNotFound
}

func (f *fakeRerankRepo) Delete(_ context.Context, id uint64) error {
	for index := range f.items {
		if f.items[index].ID == id {
			f.items = append(f.items[:index], f.items[index+1:]...)
			return nil
		}
	}
	return nil
}

func (f *fakeRerankRepo) SetEnabled(_ context.Context, id uint64, enabled bool) (bool, error) {
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

// fakeRerankProber 是探测替身：返回预设结果，并可记录这次调用收到的连接配置。
type fakeRerankProber struct {
	result rerank.ProbeResult
	err    error
}

func (f *fakeRerankProber) Probe(context.Context) (rerank.ProbeResult, error) {
	return f.result, f.err
}

// fakeRerankRuntime 是检索侧运行时的替身：记录"被配置了什么/被关了几次"。
type fakeRerankRuntime struct {
	configured []rerank.Config
	disabled   int
}

func (f *fakeRerankRuntime) Configure(cfg rerank.Config) error {
	f.configured = append(f.configured, cfg)
	return nil
}

func (f *fakeRerankRuntime) Disable() { f.disabled++ }

var rerankTestKey = []byte("0123456789abcdef0123456789abcdef")

// newRerankTestService 造一个注入了替身探测器与替身运行时的服务；
// 返回替身仓储便于断言落库结果，运行时用 impl.runtime.(*fakeRerankRuntime) 取。
func newRerankTestService(t *testing.T) (RerankSettingService, *rerankSettingService, *fakeRerankRepo) {
	t.Helper()
	repo := newFakeRerankRepo()
	svc := NewRerankSettingService(repo, rerankTestKey, &fakeRerankRuntime{}).(*rerankSettingService)
	return svc, svc, repo
}

// seedRerankSetting 直接往替身仓储塞一条配置，模拟"库里的存量数据"。
func seedRerankSetting(t *testing.T, repo *fakeRerankRepo, status string, enabled bool) *entity.RerankSetting {
	t.Helper()
	encrypted, err := appcrypto.Encrypt("sk-seed", rerankTestKey)
	if err != nil {
		t.Fatalf("加密种子密钥失败: %v", err)
	}
	item := &entity.RerankSetting{
		Name: fmt.Sprintf("测试配置-%d", time.Now().UnixNano()), BaseURL: "https://api.siliconflow.cn/v1",
		Model: "BAAI/bge-reranker-v2-m3", TimeoutSeconds: 2, TestStatus: status,
		IsEnabled: enabled, APIKeyEncrypted: encrypted,
	}
	if err := repo.Create(context.Background(), item); err != nil {
		t.Fatalf("写入种子配置失败: %v", err)
	}
	return item
}

// 新建：字段归一化、Key 加密落库（不是明文）、默认未测试未启用。
func TestRerankCreateEncryptsKeyAndDefaults(t *testing.T) {
	svc, impl, repo := newRerankTestService(t)
	ctx := context.Background()

	dto, err := svc.Create(ctx, requestdto.RerankSetting{
		Name: " 硅基流动 · bge ", BaseURL: "https://api.siliconflow.cn/v1/",
		APIKey: "sk-test", Timeout: "2s", Model: " BAAI/bge-reranker-v2-m3 ",
	})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if dto.Name != "硅基流动 · bge" || dto.BaseURL != "https://api.siliconflow.cn/v1" || dto.Model != "BAAI/bge-reranker-v2-m3" {
		t.Errorf("字段未归一化: %+v", dto)
	}
	if dto.Timeout != "2s" {
		t.Errorf("超时 = %q，期望 2s", dto.Timeout)
	}
	if !dto.APIKeyConfigured {
		t.Error("配置了 Key 时 api_key_configured 应为 true")
	}
	if dto.TestStatus != entity.RerankTestStatusUntested || dto.Enabled {
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

// 输入校验：坏值一律 400 语义且不落库。
func TestRerankCreateValidatesInput(t *testing.T) {
	cases := map[string]requestdto.RerankSetting{
		"空名称":     {BaseURL: "https://a.com/v1", Timeout: "2s", Model: "m"},
		"坏地址":     {Name: "n", BaseURL: "ftp://a.com", Timeout: "2s", Model: "m"},
		"短超时":     {Name: "n", BaseURL: "https://a.com/v1", Timeout: "200ms", Model: "m"},
		"超长超时":    {Name: "n", BaseURL: "https://a.com/v1", Timeout: "20m", Model: "m"},
		"空模型":     {Name: "n", BaseURL: "https://a.com/v1", Timeout: "2s"},
		"Key 带空格": {Name: "n", BaseURL: "https://a.com/v1", Timeout: "2s", Model: "m", APIKey: "sk x"},
		"Key 是地址": {Name: "n", BaseURL: "https://a.com/v1", Timeout: "2s", Model: "m", APIKey: "https://a.com/v1"},
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			svc, _, repo := newRerankTestService(t)
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
func TestRerankUpdateCriticalChangeResetsTestState(t *testing.T) {
	svc, _, repo := newRerankTestService(t)
	ctx := context.Background()
	item := seedRerankSetting(t, repo, entity.RerankTestStatusSuccess, true)

	dto, err := svc.Update(ctx, item.ID, requestdto.RerankSetting{
		Name: item.Name, BaseURL: item.BaseURL, Timeout: "2s", Model: "Qwen/Qwen3-Reranker-0.6B",
	})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if dto.TestStatus != entity.RerankTestStatusUntested {
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
func TestRerankUpdateNameOnlyKeepsTestState(t *testing.T) {
	svc, _, repo := newRerankTestService(t)
	ctx := context.Background()
	item := seedRerankSetting(t, repo, entity.RerankTestStatusSuccess, true)

	dto, err := svc.Update(ctx, item.ID, requestdto.RerankSetting{
		Name: "改了个名字", BaseURL: item.BaseURL, Timeout: "2s", Model: item.Model,
	})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if dto.TestStatus != entity.RerankTestStatusSuccess || !dto.Enabled {
		t.Errorf("仅改名称不应重置状态: status=%q enabled=%v", dto.TestStatus, dto.Enabled)
	}
}

// 清除 Key 也算关键变动：配置换了，测试结论作废。
func TestRerankUpdateClearKeyResets(t *testing.T) {
	svc, _, repo := newRerankTestService(t)
	ctx := context.Background()
	item := seedRerankSetting(t, repo, entity.RerankTestStatusSuccess, false)

	dto, err := svc.Update(ctx, item.ID, requestdto.RerankSetting{
		Name: item.Name, BaseURL: item.BaseURL, Timeout: "2s", Model: item.Model, ClearAPIKey: true,
	})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if dto.APIKeyConfigured {
		t.Error("清除后 api_key_configured 应为 false")
	}
	if dto.TestStatus != entity.RerankTestStatusUntested {
		t.Errorf("清除 Key 后测试状态 = %q，期望 untested", dto.TestStatus)
	}
}

// 测试成功：写回状态与探测分数，保持原启用状态，探测请求带上解密后的 Key。
func TestRerankTestSuccess(t *testing.T) {
	svc, impl, repo := newRerankTestService(t)
	ctx := context.Background()
	item := seedRerankSetting(t, repo, entity.RerankTestStatusUntested, true)

	var gotConfig rerank.Config
	impl.newProber = func(cfg rerank.Config) (rerankProber, error) {
		gotConfig = cfg
		return &fakeRerankProber{result: rerank.ProbeResult{Relevant: 0.97, Irrelevant: 0.02}}, nil
	}

	result, err := svc.Test(ctx, item.ID)
	if err != nil {
		t.Fatalf("测试失败: %v", err)
	}
	if !result.Success || !strings.Contains(result.Message, "0.97") || !strings.Contains(result.Message, "0.02") {
		t.Errorf("测试结果应带上探测分数: %+v", result)
	}
	if gotConfig.BaseURL != item.BaseURL || gotConfig.Model != item.Model || gotConfig.Timeout != 2*time.Second {
		t.Errorf("探测配置不正确: %+v", gotConfig)
	}
	if gotConfig.APIKey != "sk-seed" {
		t.Errorf("探测应带解密后的 Key，实际 %q", gotConfig.APIKey)
	}
	reloaded, _ := repo.FindByID(ctx, item.ID)
	if reloaded.TestStatus != entity.RerankTestStatusSuccess || reloaded.LastTestError != nil {
		t.Errorf("成功结果未写回: %+v", reloaded)
	}
	if !reloaded.IsEnabled {
		t.Error("测试成功不应改变启用状态")
	}
}

// 测试失败：写回失败原因并停用（测不通的配置不该继续被检索使用）。
func TestRerankTestFailure(t *testing.T) {
	svc, impl, repo := newRerankTestService(t)
	ctx := context.Background()
	item := seedRerankSetting(t, repo, entity.RerankTestStatusSuccess, true)

	impl.newProber = func(rerank.Config) (rerankProber, error) {
		return &fakeRerankProber{err: fmt.Errorf("连接失败：connection refused")}, nil
	}

	if _, err := svc.Test(ctx, item.ID); err == nil {
		t.Fatal("探测失败时应当报错")
	}
	reloaded, _ := repo.FindByID(ctx, item.ID)
	if reloaded.TestStatus != entity.RerankTestStatusFailed {
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
func TestRerankSetEnabled(t *testing.T) {
	svc, _, repo := newRerankTestService(t)
	ctx := context.Background()

	untested := seedRerankSetting(t, repo, entity.RerankTestStatusUntested, false)
	if _, err := svc.SetEnabled(ctx, untested.ID, true); err == nil {
		t.Fatal("未测试通过的配置不应允许启用")
	}
	if reloaded, _ := repo.FindByID(ctx, untested.ID); reloaded.IsEnabled {
		t.Error("被拒绝的启用不应改变库中状态")
	}

	ready := seedRerankSetting(t, repo, entity.RerankTestStatusSuccess, false)
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
func TestRerankDelete(t *testing.T) {
	svc, _, repo := newRerankTestService(t)
	ctx := context.Background()

	if err := svc.Delete(ctx, 999999); err == nil {
		t.Error("删除不存在的配置应当报错")
	}

	item := seedRerankSetting(t, repo, entity.RerankTestStatusSuccess, false)
	if err := svc.Delete(ctx, item.ID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if len(repo.items) != 0 {
		t.Errorf("删除后不应残留，实际 %d 条", len(repo.items))
	}
}

// 启用/停用即时生效：运行时被 Configure（带解密后的 Key）或被 Disable。
func TestRerankSetEnabledHotReloadsRuntime(t *testing.T) {
	svc, impl, repo := newRerankTestService(t)
	runtime := impl.runtime.(*fakeRerankRuntime)
	ctx := context.Background()

	ready := seedRerankSetting(t, repo, entity.RerankTestStatusSuccess, false)
	if _, err := svc.SetEnabled(ctx, ready.ID, true); err != nil {
		t.Fatalf("启用失败: %v", err)
	}
	if len(runtime.configured) != 1 {
		t.Fatalf("启用应把生效配置推给运行时，实际 %d 次", len(runtime.configured))
	}
	cfg := runtime.configured[0]
	if cfg.BaseURL != ready.BaseURL || cfg.Model != ready.Model || cfg.APIKey != "sk-seed" || cfg.Timeout != 2*time.Second {
		t.Errorf("推给运行时的配置不对: %+v", cfg)
	}

	if _, err := svc.SetEnabled(ctx, ready.ID, false); err != nil {
		t.Fatalf("停用失败: %v", err)
	}
	if runtime.disabled != 1 {
		t.Errorf("停用应关闭运行时，实际 %d 次", runtime.disabled)
	}
}

// 删除启用中的配置：运行时跟着关闭（不自动切到别的配置）。
func TestRerankDeleteEnabledDisablesRuntime(t *testing.T) {
	svc, impl, repo := newRerankTestService(t)
	runtime := impl.runtime.(*fakeRerankRuntime)
	ctx := context.Background()
	item := seedRerankSetting(t, repo, entity.RerankTestStatusSuccess, true)

	if err := svc.Delete(ctx, item.ID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if runtime.disabled != 1 {
		t.Errorf("删除启用中的配置后应关闭运行时，实际 %d 次", runtime.disabled)
	}
}

// 测试失败会停用配置，运行时同步关闭。
func TestRerankTestFailureDisablesRuntime(t *testing.T) {
	svc, impl, repo := newRerankTestService(t)
	runtime := impl.runtime.(*fakeRerankRuntime)
	ctx := context.Background()
	item := seedRerankSetting(t, repo, entity.RerankTestStatusSuccess, true)

	impl.newProber = func(rerank.Config) (rerankProber, error) {
		return &fakeRerankProber{err: fmt.Errorf("boom")}, nil
	}
	if _, err := svc.Test(ctx, item.ID); err == nil {
		t.Fatal("探测失败应当报错")
	}
	if runtime.disabled != 1 {
		t.Errorf("测试失败停用配置后应关闭运行时，实际 %d 次", runtime.disabled)
	}
}

// LoadActive：没有启用记录时关闭运行时；有记录时按解密后的配置生效；坏密钥报错。
func TestRerankLoadActive(t *testing.T) {
	svc, impl, repo := newRerankTestService(t)
	runtime := impl.runtime.(*fakeRerankRuntime)
	ctx := context.Background()

	if err := svc.LoadActive(ctx); err != nil {
		t.Fatalf("没有配置时应正常关闭: %v", err)
	}
	if runtime.disabled != 1 || len(runtime.configured) != 0 {
		t.Errorf("应关闭运行时: disabled=%d configured=%d", runtime.disabled, len(runtime.configured))
	}

	item := seedRerankSetting(t, repo, entity.RerankTestStatusSuccess, true)
	if err := svc.LoadActive(ctx); err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if len(runtime.configured) != 1 || runtime.configured[0].APIKey != "sk-seed" {
		t.Errorf("应把启用配置推给运行时: %+v", runtime.configured)
	}

	item.APIKeyEncrypted = "not-a-valid-ciphertext"
	if err := repo.Update(ctx, item); err != nil {
		t.Fatalf("写坏配置失败: %v", err)
	}
	if err := svc.LoadActive(ctx); err == nil {
		t.Error("密钥解不开时 LoadActive 应当报错（启动时暴露配置损坏）")
	}
}
