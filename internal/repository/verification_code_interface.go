package repository

import "context"

type VerificationCodeRepository interface {
	Issue(ctx context.Context, phone, purpose, ip, digest string) error
	Consume(ctx context.Context, phone, purpose, digest string) (bool, error)
	Delete(ctx context.Context, phone, purpose, digest string) error
}
