package entity

import (
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

func parseEntitySchema(t *testing.T, value any) *schema.Schema {
	t.Helper()

	parsed, err := schema.Parse(value, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	return parsed
}

func TestMemberCEntitiesExposeExpectedDatabaseSchema(t *testing.T) {
	tests := []struct {
		name    string
		value   any
		table   string
		columns []string
		jsonb   []string
		missing []string
	}{
		{
			name:    "classroom conversation",
			value:   &ClassroomConversation{},
			table:   "classroom_conversations",
			columns: []string{"classroom_id", "origin_scene_id", "title", "type", "status", "last_message_at", "ended_at"},
		},
		{
			name:    "conversation message",
			value:   &ConversationMessage{},
			table:   "conversation_messages",
			columns: []string{"conversation_id", "sequence_no", "sender_type", "classroom_agent_id", "sender_snapshot", "content", "status", "reply_to_message_id", "token_count", "metadata"},
			jsonb:   []string{"sender_snapshot", "metadata"},
			missing: []string{"agent_key"},
		},
		{
			name:    "orchestration run",
			value:   &OrchestrationRun{},
			table:   "orchestration_runs",
			columns: []string{"conversation_id", "trigger_message_id", "attempt_no", "trace_id", "status", "max_turns", "stop_reason", "orchestrator_version", "config_snapshot", "started_at", "finished_at", "error_message"},
			jsonb:   []string{"config_snapshot"},
		},
		{
			name:    "agent turn",
			value:   &AgentTurn{},
			table:   "agent_turns",
			columns: []string{"run_id", "turn_no", "classroom_agent_id", "agent_snapshot", "output_message_id", "status", "selection_reason", "next_action", "model", "input_tokens", "output_tokens", "started_at", "finished_at", "error_message"},
			jsonb:   []string{"agent_snapshot"},
			missing: []string{"agent_key"},
		},
		{
			name:    "context compaction",
			value:   &ContextCompaction{},
			table:   "context_compactions",
			columns: []string{"conversation_id", "previous_compaction_id", "covered_from_sequence", "covered_to_sequence", "summary", "key_points", "source_tokens", "summary_tokens", "model"},
			jsonb:   []string{"key_points"},
		},
		{
			name:    "shared context memory",
			value:   &SharedContextMemory{},
			table:   "shared_context_memories",
			columns: []string{"classroom_id", "conversation_id", "scope", "memory_type", "content", "importance", "source_message_id", "source_turn_id", "status", "superseded_by_id", "expires_at", "last_used_at"},
		},
		{
			name:    "conversation event",
			value:   &ConversationEvent{},
			table:   "conversation_events",
			columns: []string{"conversation_id", "run_id", "turn_id", "sequence_no", "event_type", "payload", "created_at", "expires_at"},
			jsonb:   []string{"payload"},
			missing: []string{"updated_at"},
		},
		{
			name:    "agent trace span",
			value:   &AgentTraceSpan{},
			table:   "agent_trace_spans",
			columns: []string{"trace_id", "span_id", "parent_span_id", "run_id", "turn_id", "kind", "name", "status", "started_at", "ended_at", "attributes", "input_summary", "output_summary", "error_message", "expires_at"},
			jsonb:   []string{"attributes"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parseEntitySchema(t, tt.value)
			if parsed.Table != tt.table {
				t.Fatalf("table = %q, want %q", parsed.Table, tt.table)
			}

			for _, column := range tt.columns {
				if parsed.FieldsByDBName[column] == nil {
					t.Errorf("missing database column %q", column)
				}
			}
			for _, column := range tt.jsonb {
				field := parsed.FieldsByDBName[column]
				if field == nil {
					continue
				}
				if got := field.TagSettings["TYPE"]; got != "jsonb" {
					t.Errorf("column %q type tag = %q, want jsonb", column, got)
				}
			}
			for _, column := range tt.missing {
				if parsed.FieldsByDBName[column] != nil {
					t.Errorf("obsolete database column %q must not be present", column)
				}
			}
		})
	}
}
