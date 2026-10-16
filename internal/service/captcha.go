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

func captchaImage(code string) string {
	var svg strings.Builder
	svg.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="144" height="48" viewBox="0 0 144 48"><rect width="144" height="48" rx="4" fill="#f1f5f9"/>`)
	for i, digit := range code {
		fmt.Fprintf(&svg, `<text x="%d" y="35" font-family="Arial,sans-serif" font-size="31" font-weight="700" fill="#%s" transform="rotate(%d %d 24)">%c</text>`,
			12+i*33, []string{"263b53", "446b59", "7b4d53", "4e557e"}[i], []int{-7, 5, -4, 7}[i], 24+i*33, digit)
	}
	svg.WriteString(`<path d="M6 14 Q65 38 138 12 M8 41 Q70 8 136 39" fill="none" stroke="#94a3b8" stroke-width="1" opacity=".6"/></svg>`)
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg.String()))
}
