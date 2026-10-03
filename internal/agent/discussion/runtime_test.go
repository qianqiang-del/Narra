package discussion

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	appcrypto "narra/pkg/crypto"

	"narra/internal/model/entity"
)

// 模型运行时工厂的测试（第 8 步第 3 区块）。
//
// 这一层要验证的是"按课堂配置凑出一套能用的模型能力"：配置读没读对、密钥解没解对、
// 地址与模型有没有真的传到底下的 HTTP 请求上。真正打网络的部分仍然用
// httptest.Server 假扮（见 openai_model_test.go 的 stubServer），不碰真实服务商。

// fakeProviderFinder 是最小的配置来源替身，可以返回预设的配置或错误。
type fakeProviderFinder struct {
	provider *Provider
	err      error

	// askedID 记下被问了哪一条，用来验证模型列表只在需要时才被读。
	askedID uint64
}

func (f *fakeProviderFinder) FindByID(_ context.Context, id uint64) (*Provider, error) {
	f.askedID = id
	if f.err != nil {
		return nil, f.err
	}
	return f.provider, nil
}

// testKey 是与下面 testProvider 里密文配套的密钥。
var testEncryptionKey = appcrypto.DeriveKey("narra-test-secret")

// testProvider 造一条配置：密钥用真加密算出来，地址稍后由用例填成假服务的。
func testProvider(t *testing.T, baseURL string, models string) *Provider {
	t.Helper()

	encrypted, err := appcrypto.Encrypt("sk-test-key", testEncryptionKey)
	if err != nil {
		t.Fatalf("加密测试密钥失败: %v", err)
	}
	return &Provider{
		BaseURL:         baseURL,
		APIKeyEncrypted: encrypted,
		TimeoutSeconds:  30,
		Models:          json.RawMessage(models),
	}
}

// ---- 装配校验 ----

// TestRuntimeFactoryRejectsMissingProviders 验证没有配置仓储就建不起来。
func TestRuntimeFactoryRejectsMissingProviders(t *testing.T) {
	factory := NewRuntimeFactory(nil, testEncryptionKey)

	if _, err := factory.Build(context.Background(), 1, "qwen-max"); err == nil {
		t.Fatal("缺少配置仓储时应当报错")
	}
}

// TestRuntimeFactoryRejectsMissingKey 验证没有解密密钥就建不起来。
func TestRuntimeFactoryRejectsMissingKey(t *testing.T) {
	factory := NewRuntimeFactory(&fakeProviderFinder{}, nil)

	if _, err := factory.Build(context.Background(), 1, "qwen-max"); err == nil {
		t.Fatal("缺少解密密钥时应当报错")
	}
}

// TestRuntimeFactoryKeepsKeyCopy 验证工厂存的是密钥的副本。
//
// 留引用的话，调用方之后原地改了那个切片，工厂读到的密文就解不开了 ——
// 而且报的是"解密失败"，看起来像数据坏了，实际是别处改了一个自己都不记得的切片。
func TestRuntimeFactoryKeepsKeyCopy(t *testing.T) {
	key := append([]byte(nil), testEncryptionKey...)
	factory := NewRuntimeFactory(&fakeProviderFinder{}, key)

	key[0] ^= 0xFF // 调用方事后动了自己那份

	stub := newStubServer(t, `{"content":"收到","next_action":"continue"}`, false)
	finder := &fakeProviderFinder{provider: testProvider(t, stub.server.URL, `["qwen-max"]`)}
	factory.providers = finder

	if _, err := factory.Build(context.Background(), 1, "qwen-max"); err != nil {
		t.Fatalf("工厂存的应是密钥副本，改调用方那份不该影响它，实际失败: %v", err)
	}
}

// ---- 配置读取 ----

// TestRuntimeFactoryUsesExplicitModelID 验证快照里选了具体模型时就用它。
func TestRuntimeFactoryUsesExplicitModelID(t *testing.T) {
	stub := newStubServer(t, `{"content":"收到","next_action":"continue"}`, false)
	finder := &fakeProviderFinder{provider: testProvider(t, stub.server.URL, `["first-model","qwen-max"]`)}

	models, err := NewRuntimeFactory(finder, testEncryptionKey).Build(context.Background(), 7, "qwen-max")
	if err != nil {
		t.Fatalf("建模型能力失败: %v", err)
	}
	if _, err := models.Model.Generate(context.Background(), GenerationRequest{Participant: testParticipant()}); err != nil {
		t.Fatalf("发起一次发言失败: %v", err)
	}

	request := stub.lastRequest(t)
	if request.body.Model != "qwen-max" {
		t.Errorf("请求里的 model = %q，期望 qwen-max（快照选了具体的就不该退到列表第一个）", request.body.Model)
	}
	if request.auth != "Bearer sk-test-key" {
		t.Errorf("Authorization = %q，期望解出来的密钥", request.auth)
	}
	if request.path != "/chat/completions" {
		t.Errorf("请求路径 = %q，期望 /chat/completions", request.path)
	}
}

func TestRuntimeFactoryCarriesPricingSnapshot(t *testing.T) {
	stub := newStubServer(t, `{"content":"收到","next_action":"end"}`, false)
	input, output := 2.0, 8.0
	pricing, _ := json.Marshal(map[string]any{"qwen-max": map[string]any{"input_per_million": input, "output_per_million": output, "currency": "CNY"}})
	provider := testProvider(t, stub.server.URL, `["qwen-max"]`)
	provider.Pricing = pricing
	models, err := NewRuntimeFactory(&fakeProviderFinder{provider: provider}, testEncryptionKey).Build(context.Background(), 1, "qwen-max")
	if err != nil {
		t.Fatalf("建模型能力失败: %v", err)
	}
	if models.ModelID != "qwen-max" || models.Pricing == nil || *models.Pricing.InputPerMillion != input || *models.Pricing.OutputPerMillion != output || models.Pricing.Currency != "CNY" {
		t.Fatalf("价格快照没有随运行模型带出: %#v", models)
	}
}

func TestPricingForModelIgnoresLegacyCatalog(t *testing.T) {
	raw := json.RawMessage(`{"old-model":{"input_per_million":2,"output_per_million":8,"currency":"CNY","source":"catalog"}}`)
	if got := pricingForModel(raw, "old-model"); got != nil {
		t.Fatalf("旧示例价格不能用于费用估算: %+v", got)
	}
}

// TestRuntimeFactoryFallsBackToFirstModel 验证快照没选模型时取列表第一个。
func TestRuntimeFactoryFallsBackToFirstModel(t *testing.T) {
	stub := newStubServer(t, `{"content":"收到","next_action":"continue"}`, false)
	finder := &fakeProviderFinder{provider: testProvider(t, stub.server.URL, `["first-model","qwen-max"]`)}

	models, err := NewRuntimeFactory(finder, testEncryptionKey).Build(context.Background(), 1, "")
	if err != nil {
		t.Fatalf("建模型能力失败: %v", err)
	}
	if _, err := models.Model.Generate(context.Background(), GenerationRequest{Participant: testParticipant()}); err != nil {
		t.Fatalf("发起一次发言失败: %v", err)
	}

	if got := stub.lastRequest(t).body.Model; got != "first-model" {
		t.Errorf("请求里的 model = %q，期望 first-model（列表第一个）", got)
	}
}

// TestRuntimeFactoryWithoutAPIKey 验证配置里没有密钥时不设 Authorization。
//
// 本地跑的 OpenAI 兼容服务（Ollama、vLLM 之类）根本不需要密钥，库里那一列就是空的。
// 真去解一个空串只会得到 base64 解码失败，把本来能跑的场景挡在门外。
func TestRuntimeFactoryWithoutAPIKey(t *testing.T) {
	stub := newStubServer(t, `{"content":"收到","next_action":"continue"}`, false)
	finder := &fakeProviderFinder{provider: &Provider{
		BaseURL:        stub.server.URL,
		Models:         json.RawMessage(`["local-model"]`),
		TimeoutSeconds: 30,
	}}

	models, err := NewRuntimeFactory(finder, testEncryptionKey).Build(context.Background(), 1, "")
	if err != nil {
		t.Fatalf("没有密钥时也该建得起来: %v", err)
	}
	if _, err := models.Model.Generate(context.Background(), GenerationRequest{Participant: testParticipant()}); err != nil {
		t.Fatalf("发起一次发言失败: %v", err)
	}

	request := stub.lastRequest(t)
	if request.auth != "" {
		t.Errorf("空密钥不该带 Authorization，实际是 %q", request.auth)
	}
	if request.body.Model != "local-model" {
		t.Errorf("请求里的 model = %q，期望 local-model", request.body.Model)
	}
}

// TestRuntimeFactorySharesOneAdapter 验证三项能力来自同一个适配器。
//
// 它们打的是同一个地址、同一个模型、同一个密钥；给三个实例除了多占两份连接池，
// 还会让"某一次发言走的是另一个客户端"这种事变得可能。
func TestRuntimeFactorySharesOneAdapter(t *testing.T) {
	stub := newStubServer(t, `{"content":"收到","next_action":"continue"}`, false)
	finder := &fakeProviderFinder{provider: testProvider(t, stub.server.URL, `["qwen-max"]`)}

	models, err := NewRuntimeFactory(finder, testEncryptionKey).Build(context.Background(), 1, "qwen-max")
	if err != nil {
		t.Fatalf("建模型能力失败: %v", err)
	}

	adapter, ok := models.Model.(*OpenAIModels)
	if !ok {
		t.Fatalf("发言能力 = %#v，期望 *OpenAIModels", models.Model)
	}
	if summarizer, ok := models.Summarizer.(*OpenAIModels); !ok || summarizer != adapter {
		t.Error("摘要能力与发言能力不是同一个适配器")
	}
	if extractor, ok := models.Extractor.(*OpenAIModels); !ok || extractor != adapter {
		t.Error("提炼能力与发言能力不是同一个适配器")
	}
}

// ---- 错误路径 ----

// TestRuntimeFactoryKeepsLookupError 验证查询失败的原因保留在错误链里。
//
// 不带 %w 的话，日志里只剩一句"读取配置失败"，分不清是没这条记录、还是数据库连不上。
func TestRuntimeFactoryKeepsLookupError(t *testing.T) {
	notFound := errors.New("record not found")
	factory := NewRuntimeFactory(&fakeProviderFinder{err: notFound}, testEncryptionKey)

	_, err := factory.Build(context.Background(), 42, "qwen-max")
	if err == nil {
		t.Fatal("查询配置失败时应当返回错误")
	}
	if !errors.Is(err, notFound) {
		t.Errorf("错误链里丢了根因：%v", err)
	}
	if !strings.Contains(err.Error(), "42") {
		t.Errorf("错误信息里没带配置 ID：%v", err)
	}
}

// TestRuntimeFactoryHandlesNilProvider 验证仓储返回空配置时不 panic。
//
// 接口约定是"找不到就返回错误"，但一个返回 (nil, nil) 的实现会让下面的
// provider.Models 直接空指针 —— 那种崩溃现场离根因太远。
func TestRuntimeFactoryHandlesNilProvider(t *testing.T) {
	factory := NewRuntimeFactory(&fakeProviderFinder{}, testEncryptionKey)

	if _, err := factory.Build(context.Background(), 1, "qwen-max"); err == nil {
		t.Fatal("配置为空时应当报错，而不是继续往下用")
	}
}

// TestRuntimeFactoryRejectsBadModelList 验证模型列表读不出可用模型时当场报错。
//
// 三种情况都让"该用哪个模型"没有答案。放过去的话，失败会推迟到第一次调模型，
// 那时运行记录已经建了，用户看到的是一场莫名其妙的失败。
func TestRuntimeFactoryRejectsBadModelList(t *testing.T) {
	cases := []struct {
		name   string
		models string
	}{
		{"不是 JSON 数组", `"qwen-max"`},
		{"空数组", `[]`},
		{"首项为空串", `["","qwen-max"]`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			finder := &fakeProviderFinder{provider: testProvider(t, "http://127.0.0.1:1", testCase.models)}
			factory := NewRuntimeFactory(finder, testEncryptionKey)

			if _, err := factory.Build(context.Background(), 1, ""); err == nil {
				t.Fatal("模型列表给不出可用模型时应当报错")
			}
		})
	}
}

// TestRuntimeFactoryRejectsBadCiphertext 验证密钥解不开时当场报错。
//
// 密文可能是用另一个环境的密钥加的（换了 JWT Secret、导库时抄漏了）。
// 不在这里拦住的话，用户会拿到一个"能建起来、但每次发言都 401"的运行时。
func TestRuntimeFactoryRejectsBadCiphertext(t *testing.T) {
	finder := &fakeProviderFinder{provider: &Provider{
		BaseURL:         "http://127.0.0.1:1",
		APIKeyEncrypted: "这不是一段 base64",
		Models:          json.RawMessage(`["qwen-max"]`),
	}}
	factory := NewRuntimeFactory(finder, testEncryptionKey)

	if _, err := factory.Build(context.Background(), 1, ""); err == nil {
		t.Fatal("密文解不开时应当报错")
	}
}

// ---- 仓储适配 ----

// fakeEntityLookup 假扮仓储，返回一条实体或一个错误。
type fakeEntityLookup struct {
	provider *entity.LLMProvider
	err      error
}

func (f *fakeEntityLookup) FindByID(_ context.Context, _ uint64) (*entity.LLMProvider, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.provider, nil
}

// TestNewEntityProviderFinderMapsFields 验证实体到本包结构的字段搬运。
//
// 少搬一个字段（比如超时）不会报错，只会让这堂课一直用 60 秒的默认值 ——
// 那种错很难从现象上看出来。
func TestNewEntityProviderFinderMapsFields(t *testing.T) {
	lookup := &fakeEntityLookup{provider: &entity.LLMProvider{
		BaseURL:         "https://api.example.com/v1",
		APIKeyEncrypted: "ciphertext",
		TimeoutSeconds:  120,
		Models:          json.RawMessage(`["qwen-max"]`),
	}}

	provider, err := NewEntityProviderFinder(lookup).FindByID(context.Background(), 1)
	if err != nil {
		t.Fatalf("取配置失败: %v", err)
	}
	if provider == nil {
		t.Fatal("应当返回一条配置")
	}
	if provider.BaseURL != "https://api.example.com/v1" {
		t.Errorf("BaseURL = %q", provider.BaseURL)
	}
	if provider.APIKeyEncrypted != "ciphertext" {
		t.Errorf("APIKeyEncrypted = %q", provider.APIKeyEncrypted)
	}
	if provider.TimeoutSeconds != 120 {
		t.Errorf("TimeoutSeconds = %d，期望 120", provider.TimeoutSeconds)
	}
	if string(provider.Models) != `["qwen-max"]` {
		t.Errorf("Models = %s", provider.Models)
	}
}

// TestNewEntityProviderFinderHandlesNilEntity 验证仓储返回空实体时不 panic。
//
// 接口约定是"找不到就返回错误"，但一个返回 (nil, nil) 的实现会让转换那一步
// 直接空指针 —— 那种崩溃现场离根因太远。
func TestNewEntityProviderFinderHandlesNilEntity(t *testing.T) {
	provider, err := NewEntityProviderFinder(&fakeEntityLookup{}).FindByID(context.Background(), 1)
	if err != nil {
		t.Fatalf("空实体不该当成错误: %v", err)
	}
	if provider != nil {
		t.Errorf("空实体应当转成 nil，实际是 %+v", provider)
	}
}

// TestNewEntityProviderFinderKeepsError 验证查询错误原样透出。
func TestNewEntityProviderFinderKeepsError(t *testing.T) {
	notFound := errors.New("record not found")
	_, err := NewEntityProviderFinder(&fakeEntityLookup{err: notFound}).FindByID(context.Background(), 1)
	if !errors.Is(err, notFound) {
		t.Errorf("错误链里丢了根因：%v", err)
	}
}
