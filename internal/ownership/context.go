package ownership

import "context"

type key struct{}

func WithOwner(ctx context.Context, ownerID uint64) context.Context {
	return context.WithValue(ctx, key{}, ownerID)
}

func FromContext(ctx context.Context) uint64 {
	ownerID, _ := ctx.Value(key{}).(uint64)
	return ownerID
}
