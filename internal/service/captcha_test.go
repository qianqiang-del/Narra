package service

import (
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"strings"
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

// TestCaptchaImageSVG 确认输出仍是可解析的 SVG、四个数字按顺序保留，
// 并且同一组数字连续两次生成的结果不同，防止随机扰动被改回固定模板。
func TestCaptchaImageSVG(t *testing.T) {
	const prefix = "data:image/svg+xml;base64,"
	image := captchaImage("0123")
	if !strings.HasPrefix(image, prefix) {
		t.Fatalf("验证码前缀不对: %.40q", image)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(image, prefix))
	if err != nil {
		t.Fatalf("验证码不是合法的 base64: %v", err)
	}

	var doc struct {
		XMLName xml.Name `xml:"svg"`
		Texts   []struct {
			X    string `xml:"x,attr"`
			Y    string `xml:"y,attr"`
			Char string `xml:",chardata"`
		} `xml:"text"`
	}
	if err := xml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("验证码不是合法的 SVG: %v", err)
	}
	if len(doc.Texts) != 4 {
		t.Fatalf("验证码字符数 = %d，期望 4", len(doc.Texts))
	}
	for i, want := range []string{"0", "1", "2", "3"} {
		if doc.Texts[i].Char != want {
			t.Fatalf("第 %d 个字符 = %q，期望 %q", i, doc.Texts[i].Char, want)
		}
		if doc.Texts[i].X == "" || doc.Texts[i].Y == "" {
			t.Fatalf("第 %d 个字符缺少坐标", i)
		}
	}

	if again := captchaImage("0123"); again == image {
		t.Fatal("同一组数字连续两次生成的验证码完全相同，随机扰动没有生效")
	}
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
