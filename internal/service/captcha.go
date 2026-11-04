package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	mathrand "math/rand/v2"
	"strings"

	"github.com/redis/go-redis/v9"
)

type CaptchaChallenge struct {
	ID    string `json:"id"`
	Image string `json:"image"`
}

type CaptchaAnswer struct {
	ID   string
	Code string
	IP   string
}

type CaptchaStore interface {
	Issue(context.Context, string, string, string) error
	Consume(context.Context, string, string) (bool, error)
}

type redisCaptchaStore struct{ client *redis.Client }

func NewRedisCaptchaStore(client *redis.Client) CaptchaStore {
	if client == nil {
		return nil
	}
	return &redisCaptchaStore{client: client}
}

const issueCaptchaScript = `
local n = redis.call('INCR', KEYS[2])
if n == 1 then redis.call('EXPIRE', KEYS[2], 60) end
if n > 5 then return 0 end
redis.call('SET', KEYS[1], ARGV[1], 'EX', 120)
return 1`

func (r *redisCaptchaStore) Issue(ctx context.Context, id, ip, digest string) error {
	result, err := r.client.Eval(ctx, issueCaptchaScript,
		[]string{"auth:captcha:" + id, "auth:captcha:issue:" + keyDigest(ip)}, digest).Int()
	if err != nil {
		return err
	}
	if result == 0 {
		return ErrCaptchaRateLimited
	}
	return nil
}

const consumeCaptchaScript = `
local expected = redis.call('GET', KEYS[1])
if not expected then return 0 end
if expected == ARGV[1] then
  redis.call('DEL', KEYS[1], KEYS[2])
  return 1
end
local n = redis.call('INCR', KEYS[2])
if n == 1 then redis.call('EXPIRE', KEYS[2], 120) end
if n >= 3 then redis.call('DEL', KEYS[1], KEYS[2]) end
return 0`

func (r *redisCaptchaStore) Consume(ctx context.Context, id, digest string) (bool, error) {
	result, err := r.client.Eval(ctx, consumeCaptchaScript,
		[]string{"auth:captcha:" + id, "auth:captcha:attempts:" + id}, digest).Int()
	return result == 1, err
}

func keyDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func captchaDigest(secret []byte, id, ip, answer string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(id + ":" + ip + ":" + answer))
	return hex.EncodeToString(mac.Sum(nil))
}

func newCaptcha() (string, string, error) {
	var token [24]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", "", err
	}
	n, err := rand.Int(rand.Reader, big.NewInt(10_000))
	if err != nil {
		return "", "", err
	}
	return base64.RawURLEncoding.EncodeToString(token[:]), fmt.Sprintf("%04d", n.Int64()), nil
}

var (
	captchaFonts = []string{
		"Arial, Helvetica, sans-serif",
		"Verdana, Geneva, sans-serif",
		"Tahoma, Geneva, sans-serif",
		"Trebuchet MS, Helvetica, sans-serif",
		"Courier New, monospace",
	}
	captchaWeights = []string{"600", "700", "800"}
	captchaColors  = []string{
		"263b53", "446b59", "7b4d53", "4e557e",
		"374151", "1f4d5c", "5b3a29", "5d3f6b",
	}
	captchaBackgrounds = []string{"f1f5f9", "eef2f7", "f3f4f6", "f5f5f4"}
	captchaNoiseColors = []string{"64748b", "94a3b8", "7c8ba1", "cbd5e1"}
)

// captchaImage 把 4 位数字画成一张带随机扰动的 SVG。
// 每个字符的位置、字号、旋转、字体和颜色都逐次随机，再叠加噪点与干扰曲线；
// 画布尺寸保持固定，底色只在几种浅灰之间切换，避免登录页布局跳动。
func captchaImage(code string) string {
	var svg strings.Builder
	background := captchaBackgrounds[mathrand.IntN(len(captchaBackgrounds))]
	svg.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="144" height="48" viewBox="0 0 144 48">`)
	fmt.Fprintf(&svg, `<rect width="144" height="48" rx="4" fill="#%s"/>`, background)

	for i := 0; i < 24+mathrand.IntN(12); i++ {
		fmt.Fprintf(&svg, `<circle cx="%.1f" cy="%.1f" r="%.1f" fill="#%s" opacity="%.2f"/>`,
			mathrand.Float64()*144,
			mathrand.Float64()*48,
			0.5+mathrand.Float64()*1.3,
			captchaNoiseColors[mathrand.IntN(len(captchaNoiseColors))],
			0.12+mathrand.Float64()*0.22,
		)
	}

	for i, digit := range code {
		centerX := 24 + i*33 + mathrand.IntN(9) - 4
		centerY := 24 + mathrand.IntN(5) - 2
		fontSize := 26 + mathrand.IntN(8)
		baseline := float64(centerY) + float64(fontSize)*0.36
		fmt.Fprintf(&svg,
			`<text x="%d" y="%.1f" text-anchor="middle" font-family="%s" font-size="%d" font-weight="%s" fill="#%s" transform="rotate(%d %d %d)">%c</text>`,
			centerX, baseline,
			captchaFonts[mathrand.IntN(len(captchaFonts))],
			fontSize,
			captchaWeights[mathrand.IntN(len(captchaWeights))],
			captchaColors[mathrand.IntN(len(captchaColors))],
			mathrand.IntN(29)-14, centerX, centerY,
			digit,
		)
	}

	for i := 0; i < 2+mathrand.IntN(2); i++ {
		fmt.Fprintf(&svg,
			`<path d="M%.1f %.1f Q%.1f %.1f %.1f %.1f" fill="none" stroke="#%s" stroke-width="%.1f" opacity="%.2f"/>`,
			mathrand.Float64()*18,
			6+mathrand.Float64()*36,
			30+mathrand.Float64()*84,
			mathrand.Float64()*48,
			126+mathrand.Float64()*18,
			6+mathrand.Float64()*36,
			captchaNoiseColors[mathrand.IntN(len(captchaNoiseColors))],
			0.8+mathrand.Float64()*0.8,
			0.35+mathrand.Float64()*0.3,
		)
	}

	svg.WriteString(`</svg>`)
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg.String()))
}
