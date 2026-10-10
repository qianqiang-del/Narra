package mcp

import (
	"context"
	"fmt"
	"narra/internal/ownership"
	"strings"
)

type ownerContextKey struct{}

// WithOwner scopes remote MCP tools to one account. Unprefixed servers are public defaults.
func WithOwner(ctx context.Context, ownerID uint64) context.Context {
	return ownership.WithOwner(context.WithValue(ctx, ownerContextKey{}, ownerID), ownerID)
}

func visibleToOwner(ctx context.Context, serverID string) bool {
	if !strings.HasPrefix(serverID, "user_") {
		return true
	}
	ownerID, ok := ctx.Value(ownerContextKey{}).(uint64)
	return ok && strings.HasPrefix(serverID, fmt.Sprintf("user_%d_", ownerID))
}
