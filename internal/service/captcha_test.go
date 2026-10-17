package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// 这份文件补齐 auth_service_test.go 依赖的两个测试桩。
// 验证码改造（151/152 号改动）把服务层的 CaptchaStore 换成了
// Issue/Consume 两段式接口，但测试桩没有随之补上，测试包一直编译不过。

// testCaptchaIP 是两个桩共用的请求方 IP。签发与校验必须用同一个值：
// digest 是 HMAC(secret, id + ":" + ip + ":" + answer)，换了 IP 就对不上。
const testCaptchaIP = "127.0.0.1"

// captchaStoreFake 是 CaptchaStore 的内存替身：只保存 Issue 收到的摘要，
// Consume 逐位比对后一次性消费。
type captchaStoreFake struct {
	mu      sync.Mutex
	digests map[string]string
}

var _ CaptchaStore = (*captchaStoreFake)(nil)

func (f *captchaStoreFake) Issue(_ context.Context, id, _, digest string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.digests == nil {
		f.digests = make(map[string]string)
	}
	f.digests[id] = digest
	return nil
}

func (f *captchaStoreFake) Consume(_ context.Context, id, digest string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	expected, ok := f.digests[id]
	if !ok || expected != digest {
		return false, nil
	}
	delete(f.digests, id)
	return true, nil
}

// digestOf 返回某个 challenge 保存的摘要，供测试反推明码用。
func (f *captchaStoreFake) digestOf(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.digests[id]
}

// newTestCaptchaAnswer 走真实的签发流程拿一个 challenge，再把 4 位明码
// 穷举回推出来（一共一万种，毫秒级）。这样测试传的仍是真实的 CaptchaAnswer
// 结构，而不是绕过校验或伪造摘要。
func newTestCaptchaAnswer(t *testing.T, svc *authService) CaptchaAnswer {
	t.Helper()

	challenge, err := svc.NewCaptcha(context.Background(), testCaptchaIP)
	if err != nil {
		t.Fatalf("签发图形验证码失败: %v", err)
	}
	fake, ok := svc.captcha.(*captchaStoreFake)
	if !ok {
		t.Fatalf("用例没有装图形验证码替身，实际是 %T", svc.captcha)
	}
	digest := fake.digestOf(challenge.ID)
	for candidate := 0; candidate < 10_000; candidate++ {
		code := fmt.Sprintf("%04d", candidate)
		if captchaDigest(svc.secret, challenge.ID, testCaptchaIP, code) == digest {
			return CaptchaAnswer{ID: challenge.ID, Code: code, IP: testCaptchaIP}
		}
	}
	t.Fatal("反推验证码明码失败")
	return CaptchaAnswer{}
}
