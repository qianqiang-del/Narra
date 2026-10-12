package repository

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

type refreshTokenRepository struct{ redis *redis.Client }

func NewRefreshTokenRepository(client *redis.Client) RefreshTokenRepository {
	return &refreshTokenRepository{redis: client}
}

func refreshTokenKey(digest string) string { return "auth:refresh:" + digest }

func (r *refreshTokenRepository) Store(ctx context.Context, digest string, userID uint64, ttl time.Duration) error {
	return r.redis.Set(ctx, refreshTokenKey(digest), strconv.FormatUint(userID, 10), ttl).Err()
}

const rotateRefreshTokenScript = `
local user = redis.call('GET', KEYS[1])
if not user then return 0 end
redis.call('SET', KEYS[2], user, 'EX', ARGV[1])
redis.call('DEL', KEYS[1])
return tonumber(user)`

func (r *refreshTokenRepository) Rotate(ctx context.Context, oldDigest, newDigest string, ttl time.Duration) (uint64, error) {
	userID, err := r.redis.Eval(ctx, rotateRefreshTokenScript,
		[]string{refreshTokenKey(oldDigest), refreshTokenKey(newDigest)}, int64(ttl.Seconds())).Int64()
	if err != nil {
		return 0, fmt.Errorf("rotate refresh token: %w", err)
	}
	return uint64(userID), nil
}

func (r *refreshTokenRepository) Revoke(ctx context.Context, digest string) error {
	return r.redis.Del(ctx, refreshTokenKey(digest)).Err()
}
