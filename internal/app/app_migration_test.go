package app

import "testing"

type tableNamer interface {
	TableName() string
}

func TestDatabaseEntitiesFollowForeignKeyDependencyOrder(t *testing.T) {
	want := []string{
		"folders",
		"classrooms",
		"preset_agents",
		"classroom_agents",
		"scenes",
		"scene_segments",
		"classroom_conversations",
		"conversation_messages",
		"context_compactions",
		"orchestration_runs",
		"agent_turns",
		"shared_context_memories",
		"conversation_events",
		"agent_trace_spans",
	}

	entities := databaseEntities()
	if len(entities) != len(want) {
		t.Fatalf("entity count = %d, want %d", len(entities), len(want))
	}

	for i, entity := range entities {
		namer, ok := entity.(tableNamer)
		if !ok {
			t.Fatalf("entity %d (%T) does not expose TableName", i, entity)
		}
		if got := namer.TableName(); got != want[i] {
			t.Errorf("entity %d table = %q, want %q", i, got, want[i])
		}
	}
}
