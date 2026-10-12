package repository

import (
	"context"
	"time"
)

type RefreshTokenRepository interface {
	Store(ctx context.Context, digest string, userID uint64, ttl time.Duration) error
	Rotate(ctx context.Context, oldDigest, newDigest string, ttl time.Duration) (uint64, error)
	Revoke(ctx context.Context, digest string) error
}
