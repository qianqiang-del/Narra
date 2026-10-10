package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

var ErrVerificationCodeRateLimited = errors.New("验证码发送过于频繁，请稍后再试")

type verificationCodeRepository struct{ redis *redis.Client }

func NewVerificationCodeRepository(client *redis.Client) VerificationCodeRepository {
	if client == nil {
		return nil
	}
	return &verificationCodeRepository{redis: client}
}

func verificationCodeKey(phone, purpose string) string {
	return "auth:code:" + purpose + ":" + phone
}

const issueVerificationCodeScript = `
if redis.call('EXISTS', KEYS[2]) == 1 then return 0 end
if tonumber(redis.call('GET', KEYS[3]) or '0') >= 10 then return 0 end
if tonumber(redis.call('GET', KEYS[4]) or '0') >= 20 then return 0 end
local phoneCount = redis.call('INCR', KEYS[3])
if phoneCount == 1 then redis.call('EXPIRE', KEYS[3], 86400) end
local ipCount = redis.call('INCR', KEYS[4])
if ipCount == 1 then redis.call('EXPIRE', KEYS[4], 3600) end
redis.call('SET', KEYS[1], ARGV[1], 'EX', 300)
redis.call('SET', KEYS[2], '1', 'EX', 60)
redis.call('DEL', KEYS[5])
return 1`

func (r *verificationCodeRepository) Issue(ctx context.Context, phone, purpose, ip, digest string) error {
	key := verificationCodeKey(phone, purpose)
	keys := []string{key, key + ":cooldown", "auth:phone:" + phone, "auth:ip:" + ip, key + ":attempts"}
	result, err := r.redis.Eval(ctx, issueVerificationCodeScript, keys, digest).Int()
	if err != nil {
		return fmt.Errorf("store verification code: %w", err)
	}
	if result == 0 {
		return ErrVerificationCodeRateLimited
	}
	return nil
}

func (r *verificationCodeRepository) Exists(ctx context.Context, phone, purpose string) (bool, error) {
	count, err := r.redis.Exists(ctx, verificationCodeKey(phone, purpose)).Result()
	return count > 0, err
}

const consumeVerificationCodeScript = `
local expected = redis.call('GET', KEYS[1])
if not expected then return 0 end
if expected == ARGV[1] then
  redis.call('DEL', KEYS[1], KEYS[2])
  return 1
end
local attempts = redis.call('INCR', KEYS[2])
if attempts == 1 then redis.call('EXPIRE', KEYS[2], 300) end
if attempts >= 5 then redis.call('DEL', KEYS[1], KEYS[2]) end
return 0`

func (r *verificationCodeRepository) Consume(ctx context.Context, phone, purpose, digest string) (bool, error) {
	key := verificationCodeKey(phone, purpose)
	result, err := r.redis.Eval(ctx, consumeVerificationCodeScript, []string{key, key + ":attempts"}, digest).Int()
	if err != nil {
		return false, fmt.Errorf("verify code: %w", err)
	}
	return result == 1, nil
}

const deleteVerificationCodeScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('DEL', KEYS[1], KEYS[2], KEYS[3])
return 1`

func (r *verificationCodeRepository) Delete(ctx context.Context, phone, purpose, digest string) error {
	key := verificationCodeKey(phone, purpose)
	return r.redis.Eval(ctx, deleteVerificationCodeScript, []string{key, key + ":attempts", key + ":cooldown"}, digest).Err()
}
