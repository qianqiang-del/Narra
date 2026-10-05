package mcp

import (
	"context"
	"encoding/json"
	"narra/pkg/config"
	"testing"
)

func TestDiscussionToolsOnlyExposeReadOnlyRemoteTools(t *testing.T) {
	manager := NewManager(config.AppConfig{})
	manager.registry, _ = NewRegistry([]ToolDescriptor{
		{ServerID: "web", RemoteName: "tavily_search", Description: "Search", InputSchema: json.RawMessage(`{"type":"object"}`), ReadOnly: true},
		{ServerID: "web", RemoteName: "delete_item", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	for _, enabled := range []bool{false, true} {
		tools, err := manager.DiscussionTools(context.Background(), enabled)
		if err != nil {
			t.Fatal(err)
		}
		expected := 0
		if enabled {
			expected = 1
		}
		if len(tools) != expected {
			t.Fatalf("enabled=%v got=%d", enabled, len(tools))
		}
	}
	// The teammates' API is deliberately unchanged.
	tools, err := manager.EinoTools(context.Background(), true)
	if err != nil || len(tools) != 2 {
		t.Fatalf("shared behavior changed: %d %v", len(tools), err)
	}
}
