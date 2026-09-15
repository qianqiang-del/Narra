-- 0003_member_c_constraints.sql
-- 成员 C：多 Agent 编排、上下文、共享记忆、SSE 重放和本地链路追踪。
-- AutoMigrate 创建表后执行本文件。脚本可重复执行，不修改现有模块的表约束。

BEGIN;

CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- classroom_conversations
ALTER TABLE classroom_conversations DROP CONSTRAINT IF EXISTS classroom_conversations_type_check;
ALTER TABLE classroom_conversations ADD CONSTRAINT classroom_conversations_type_check
    CHECK (type IN ('qa', 'discussion', 'lecture'));

ALTER TABLE classroom_conversations DROP CONSTRAINT IF EXISTS classroom_conversations_status_check;
ALTER TABLE classroom_conversations ADD CONSTRAINT classroom_conversations_status_check
    CHECK (status IN ('active', 'closed'));

ALTER TABLE classroom_conversations DROP CONSTRAINT IF EXISTS classroom_conversations_classroom_id_fkey;
ALTER TABLE classroom_conversations ADD CONSTRAINT classroom_conversations_classroom_id_fkey
    FOREIGN KEY (classroom_id) REFERENCES classrooms (id) ON DELETE CASCADE;

ALTER TABLE classroom_conversations DROP CONSTRAINT IF EXISTS classroom_conversations_origin_scene_id_fkey;
ALTER TABLE classroom_conversations ADD CONSTRAINT classroom_conversations_origin_scene_id_fkey
    FOREIGN KEY (origin_scene_id) REFERENCES scenes (id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_classroom_conversations_recent
    ON classroom_conversations (classroom_id, last_message_at DESC);

DROP TRIGGER IF EXISTS trg_classroom_conversations_updated_at ON classroom_conversations;
CREATE TRIGGER trg_classroom_conversations_updated_at
    BEFORE UPDATE ON classroom_conversations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- conversation_messages
ALTER TABLE conversation_messages DROP CONSTRAINT IF EXISTS conversation_messages_sequence_no_check;
ALTER TABLE conversation_messages ADD CONSTRAINT conversation_messages_sequence_no_check
    CHECK (sequence_no >= 1);

ALTER TABLE conversation_messages DROP CONSTRAINT IF EXISTS conversation_messages_token_count_check;
ALTER TABLE conversation_messages ADD CONSTRAINT conversation_messages_token_count_check
    CHECK (token_count >= 0);

ALTER TABLE conversation_messages DROP CONSTRAINT IF EXISTS conversation_messages_sender_type_check;
ALTER TABLE conversation_messages ADD CONSTRAINT conversation_messages_sender_type_check
    CHECK (sender_type IN ('user', 'agent', 'system'));

ALTER TABLE conversation_messages DROP CONSTRAINT IF EXISTS conversation_messages_status_check;
ALTER TABLE conversation_messages ADD CONSTRAINT conversation_messages_status_check
    CHECK (status IN ('streaming', 'completed', 'failed', 'cancelled'));

ALTER TABLE conversation_messages DROP CONSTRAINT IF EXISTS conversation_messages_conversation_sequence_key;
ALTER TABLE conversation_messages ADD CONSTRAINT conversation_messages_conversation_sequence_key
    UNIQUE (conversation_id, sequence_no);

ALTER TABLE conversation_messages DROP CONSTRAINT IF EXISTS conversation_messages_conversation_id_fkey;
ALTER TABLE conversation_messages ADD CONSTRAINT conversation_messages_conversation_id_fkey
    FOREIGN KEY (conversation_id) REFERENCES classroom_conversations (id) ON DELETE CASCADE;

ALTER TABLE conversation_messages DROP CONSTRAINT IF EXISTS conversation_messages_classroom_agent_id_fkey;
ALTER TABLE conversation_messages ADD CONSTRAINT conversation_messages_classroom_agent_id_fkey
    FOREIGN KEY (classroom_agent_id) REFERENCES classroom_agents (id) ON DELETE SET NULL;

ALTER TABLE conversation_messages DROP CONSTRAINT IF EXISTS conversation_messages_reply_to_message_id_fkey;
ALTER TABLE conversation_messages ADD CONSTRAINT conversation_messages_reply_to_message_id_fkey
    FOREIGN KEY (reply_to_message_id) REFERENCES conversation_messages (id) ON DELETE SET NULL;

DROP TRIGGER IF EXISTS trg_conversation_messages_updated_at ON conversation_messages;
CREATE TRIGGER trg_conversation_messages_updated_at
    BEFORE UPDATE ON conversation_messages
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- context_compactions
ALTER TABLE context_compactions DROP CONSTRAINT IF EXISTS context_compactions_sequence_range_check;
ALTER TABLE context_compactions ADD CONSTRAINT context_compactions_sequence_range_check
    CHECK (covered_from_sequence >= 1 AND covered_to_sequence >= covered_from_sequence);

ALTER TABLE context_compactions DROP CONSTRAINT IF EXISTS context_compactions_token_count_check;
ALTER TABLE context_compactions ADD CONSTRAINT context_compactions_token_count_check
    CHECK (source_tokens > 0 AND summary_tokens >= 0 AND summary_tokens < source_tokens);

ALTER TABLE context_compactions DROP CONSTRAINT IF EXISTS context_compactions_conversation_covered_key;
ALTER TABLE context_compactions ADD CONSTRAINT context_compactions_conversation_covered_key
    UNIQUE (conversation_id, covered_to_sequence);

ALTER TABLE context_compactions DROP CONSTRAINT IF EXISTS context_compactions_conversation_id_fkey;
ALTER TABLE context_compactions ADD CONSTRAINT context_compactions_conversation_id_fkey
    FOREIGN KEY (conversation_id) REFERENCES classroom_conversations (id) ON DELETE CASCADE;

ALTER TABLE context_compactions DROP CONSTRAINT IF EXISTS context_compactions_previous_id_fkey;
ALTER TABLE context_compactions ADD CONSTRAINT context_compactions_previous_id_fkey
    FOREIGN KEY (previous_compaction_id) REFERENCES context_compactions (id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_context_compactions_latest
    ON context_compactions (conversation_id, covered_to_sequence DESC);

DROP TRIGGER IF EXISTS trg_context_compactions_updated_at ON context_compactions;
CREATE TRIGGER trg_context_compactions_updated_at
    BEFORE UPDATE ON context_compactions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- orchestration_runs
ALTER TABLE orchestration_runs DROP CONSTRAINT IF EXISTS orchestration_runs_status_check;
ALTER TABLE orchestration_runs ADD CONSTRAINT orchestration_runs_status_check
    CHECK (status IN ('queued', 'running', 'waiting_user', 'completed', 'failed', 'cancelled'));

ALTER TABLE orchestration_runs DROP CONSTRAINT IF EXISTS orchestration_runs_stop_reason_check;
ALTER TABLE orchestration_runs ADD CONSTRAINT orchestration_runs_stop_reason_check
    CHECK (stop_reason IS NULL OR stop_reason IN ('completed', 'waiting_user', 'max_turns', 'error', 'cancelled'));

ALTER TABLE orchestration_runs DROP CONSTRAINT IF EXISTS orchestration_runs_attempt_no_check;
ALTER TABLE orchestration_runs ADD CONSTRAINT orchestration_runs_attempt_no_check
    CHECK (attempt_no >= 1);

ALTER TABLE orchestration_runs DROP CONSTRAINT IF EXISTS orchestration_runs_max_turns_check;
ALTER TABLE orchestration_runs ADD CONSTRAINT orchestration_runs_max_turns_check
    CHECK (max_turns BETWEEN 1 AND 50);

ALTER TABLE orchestration_runs DROP CONSTRAINT IF EXISTS orchestration_runs_time_range_check;
ALTER TABLE orchestration_runs ADD CONSTRAINT orchestration_runs_time_range_check
    CHECK (finished_at IS NULL OR started_at IS NULL OR finished_at >= started_at);

ALTER TABLE orchestration_runs DROP CONSTRAINT IF EXISTS orchestration_runs_trace_id_format_check;
ALTER TABLE orchestration_runs ADD CONSTRAINT orchestration_runs_trace_id_format_check
    CHECK (trace_id ~ '^[0-9a-f]{32}$');

ALTER TABLE orchestration_runs DROP CONSTRAINT IF EXISTS orchestration_runs_trigger_attempt_key;
ALTER TABLE orchestration_runs ADD CONSTRAINT orchestration_runs_trigger_attempt_key
    UNIQUE (trigger_message_id, attempt_no);

ALTER TABLE orchestration_runs DROP CONSTRAINT IF EXISTS orchestration_runs_trace_id_key;
ALTER TABLE orchestration_runs ADD CONSTRAINT orchestration_runs_trace_id_key
    UNIQUE (trace_id);

ALTER TABLE orchestration_runs DROP CONSTRAINT IF EXISTS orchestration_runs_conversation_id_fkey;
ALTER TABLE orchestration_runs ADD CONSTRAINT orchestration_runs_conversation_id_fkey
    FOREIGN KEY (conversation_id) REFERENCES classroom_conversations (id) ON DELETE CASCADE;

ALTER TABLE orchestration_runs DROP CONSTRAINT IF EXISTS orchestration_runs_trigger_message_id_fkey;
ALTER TABLE orchestration_runs ADD CONSTRAINT orchestration_runs_trigger_message_id_fkey
    FOREIGN KEY (trigger_message_id) REFERENCES conversation_messages (id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_orchestration_runs_recent
    ON orchestration_runs (conversation_id, created_at DESC);

DROP TRIGGER IF EXISTS trg_orchestration_runs_updated_at ON orchestration_runs;
CREATE TRIGGER trg_orchestration_runs_updated_at
    BEFORE UPDATE ON orchestration_runs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- agent_turns
ALTER TABLE agent_turns DROP CONSTRAINT IF EXISTS agent_turns_status_check;
ALTER TABLE agent_turns ADD CONSTRAINT agent_turns_status_check
    CHECK (status IN ('scheduled', 'running', 'completed', 'failed', 'cancelled'));

ALTER TABLE agent_turns DROP CONSTRAINT IF EXISTS agent_turns_next_action_check;
ALTER TABLE agent_turns ADD CONSTRAINT agent_turns_next_action_check
    CHECK (next_action IS NULL OR next_action IN ('continue', 'switch_agent', 'ask_user', 'end'));

ALTER TABLE agent_turns DROP CONSTRAINT IF EXISTS agent_turns_counts_check;
ALTER TABLE agent_turns ADD CONSTRAINT agent_turns_counts_check
    CHECK (turn_no >= 1 AND input_tokens >= 0 AND output_tokens >= 0);

ALTER TABLE agent_turns DROP CONSTRAINT IF EXISTS agent_turns_time_range_check;
ALTER TABLE agent_turns ADD CONSTRAINT agent_turns_time_range_check
    CHECK (finished_at IS NULL OR started_at IS NULL OR finished_at >= started_at);

ALTER TABLE agent_turns DROP CONSTRAINT IF EXISTS agent_turns_run_turn_key;
ALTER TABLE agent_turns ADD CONSTRAINT agent_turns_run_turn_key
    UNIQUE (run_id, turn_no);

ALTER TABLE agent_turns DROP CONSTRAINT IF EXISTS agent_turns_output_message_key;
ALTER TABLE agent_turns ADD CONSTRAINT agent_turns_output_message_key
    UNIQUE (output_message_id);

ALTER TABLE agent_turns DROP CONSTRAINT IF EXISTS agent_turns_run_id_fkey;
ALTER TABLE agent_turns ADD CONSTRAINT agent_turns_run_id_fkey
    FOREIGN KEY (run_id) REFERENCES orchestration_runs (id) ON DELETE CASCADE;

ALTER TABLE agent_turns DROP CONSTRAINT IF EXISTS agent_turns_classroom_agent_id_fkey;
ALTER TABLE agent_turns ADD CONSTRAINT agent_turns_classroom_agent_id_fkey
    FOREIGN KEY (classroom_agent_id) REFERENCES classroom_agents (id) ON DELETE SET NULL;

ALTER TABLE agent_turns DROP CONSTRAINT IF EXISTS agent_turns_output_message_id_fkey;
ALTER TABLE agent_turns ADD CONSTRAINT agent_turns_output_message_id_fkey
    FOREIGN KEY (output_message_id) REFERENCES conversation_messages (id) ON DELETE SET NULL;

DROP TRIGGER IF EXISTS trg_agent_turns_updated_at ON agent_turns;
CREATE TRIGGER trg_agent_turns_updated_at
    BEFORE UPDATE ON agent_turns
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- shared_context_memories
ALTER TABLE shared_context_memories DROP CONSTRAINT IF EXISTS shared_context_memories_scope_check;
ALTER TABLE shared_context_memories ADD CONSTRAINT shared_context_memories_scope_check
    CHECK (
        (scope = 'classroom' AND conversation_id IS NULL) OR
        (scope = 'conversation' AND conversation_id IS NOT NULL)
    );

ALTER TABLE shared_context_memories DROP CONSTRAINT IF EXISTS shared_context_memories_type_check;
ALTER TABLE shared_context_memories ADD CONSTRAINT shared_context_memories_type_check
    CHECK (memory_type IN ('fact', 'decision', 'learning_state', 'preference', 'open_question'));

ALTER TABLE shared_context_memories DROP CONSTRAINT IF EXISTS shared_context_memories_status_check;
ALTER TABLE shared_context_memories ADD CONSTRAINT shared_context_memories_status_check
    CHECK (status IN ('active', 'superseded', 'retracted'));

ALTER TABLE shared_context_memories DROP CONSTRAINT IF EXISTS shared_context_memories_importance_check;
ALTER TABLE shared_context_memories ADD CONSTRAINT shared_context_memories_importance_check
    CHECK (importance BETWEEN 1 AND 5);

ALTER TABLE shared_context_memories DROP CONSTRAINT IF EXISTS shared_context_memories_not_self_superseded_check;
ALTER TABLE shared_context_memories ADD CONSTRAINT shared_context_memories_not_self_superseded_check
    CHECK (superseded_by_id IS NULL OR superseded_by_id <> id);

ALTER TABLE shared_context_memories DROP CONSTRAINT IF EXISTS shared_context_memories_classroom_id_fkey;
ALTER TABLE shared_context_memories ADD CONSTRAINT shared_context_memories_classroom_id_fkey
    FOREIGN KEY (classroom_id) REFERENCES classrooms (id) ON DELETE CASCADE;

ALTER TABLE shared_context_memories DROP CONSTRAINT IF EXISTS shared_context_memories_conversation_id_fkey;
ALTER TABLE shared_context_memories ADD CONSTRAINT shared_context_memories_conversation_id_fkey
    FOREIGN KEY (conversation_id) REFERENCES classroom_conversations (id) ON DELETE CASCADE;

ALTER TABLE shared_context_memories DROP CONSTRAINT IF EXISTS shared_context_memories_source_message_id_fkey;
ALTER TABLE shared_context_memories ADD CONSTRAINT shared_context_memories_source_message_id_fkey
    FOREIGN KEY (source_message_id) REFERENCES conversation_messages (id) ON DELETE SET NULL;

ALTER TABLE shared_context_memories DROP CONSTRAINT IF EXISTS shared_context_memories_source_turn_id_fkey;
ALTER TABLE shared_context_memories ADD CONSTRAINT shared_context_memories_source_turn_id_fkey
    FOREIGN KEY (source_turn_id) REFERENCES agent_turns (id) ON DELETE SET NULL;

ALTER TABLE shared_context_memories DROP CONSTRAINT IF EXISTS shared_context_memories_superseded_by_id_fkey;
ALTER TABLE shared_context_memories ADD CONSTRAINT shared_context_memories_superseded_by_id_fkey
    FOREIGN KEY (superseded_by_id) REFERENCES shared_context_memories (id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_shared_memories_classroom
    ON shared_context_memories (classroom_id, scope, status, importance DESC);

CREATE INDEX IF NOT EXISTS idx_shared_memories_conversation
    ON shared_context_memories (conversation_id, status, importance DESC);

DROP TRIGGER IF EXISTS trg_shared_context_memories_updated_at ON shared_context_memories;
CREATE TRIGGER trg_shared_context_memories_updated_at
    BEFORE UPDATE ON shared_context_memories
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- conversation_events
ALTER TABLE conversation_events ALTER COLUMN expires_at
    SET DEFAULT (CURRENT_TIMESTAMP + INTERVAL '7 days');

ALTER TABLE conversation_events DROP CONSTRAINT IF EXISTS conversation_events_sequence_no_check;
ALTER TABLE conversation_events ADD CONSTRAINT conversation_events_sequence_no_check
    CHECK (sequence_no >= 1);

ALTER TABLE conversation_events DROP CONSTRAINT IF EXISTS conversation_events_conversation_sequence_key;
ALTER TABLE conversation_events ADD CONSTRAINT conversation_events_conversation_sequence_key
    UNIQUE (conversation_id, sequence_no);

ALTER TABLE conversation_events DROP CONSTRAINT IF EXISTS conversation_events_conversation_id_fkey;
ALTER TABLE conversation_events ADD CONSTRAINT conversation_events_conversation_id_fkey
    FOREIGN KEY (conversation_id) REFERENCES classroom_conversations (id) ON DELETE CASCADE;

ALTER TABLE conversation_events DROP CONSTRAINT IF EXISTS conversation_events_run_id_fkey;
ALTER TABLE conversation_events ADD CONSTRAINT conversation_events_run_id_fkey
    FOREIGN KEY (run_id) REFERENCES orchestration_runs (id) ON DELETE SET NULL;

ALTER TABLE conversation_events DROP CONSTRAINT IF EXISTS conversation_events_turn_id_fkey;
ALTER TABLE conversation_events ADD CONSTRAINT conversation_events_turn_id_fkey
    FOREIGN KEY (turn_id) REFERENCES agent_turns (id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_conversation_events_expires_at
    ON conversation_events (expires_at);

-- agent_trace_spans
ALTER TABLE agent_trace_spans ALTER COLUMN expires_at
    SET DEFAULT (CURRENT_TIMESTAMP + INTERVAL '30 days');

ALTER TABLE agent_trace_spans DROP CONSTRAINT IF EXISTS agent_trace_spans_kind_check;
ALTER TABLE agent_trace_spans ADD CONSTRAINT agent_trace_spans_kind_check
    CHECK (kind IN ('orchestration', 'director', 'agent', 'model', 'tool', 'memory', 'sse'));

ALTER TABLE agent_trace_spans DROP CONSTRAINT IF EXISTS agent_trace_spans_status_check;
ALTER TABLE agent_trace_spans ADD CONSTRAINT agent_trace_spans_status_check
    CHECK (status IN ('running', 'ok', 'error', 'cancelled'));

ALTER TABLE agent_trace_spans DROP CONSTRAINT IF EXISTS agent_trace_spans_id_format_check;
ALTER TABLE agent_trace_spans ADD CONSTRAINT agent_trace_spans_id_format_check
    CHECK (
        trace_id ~ '^[0-9a-f]{32}$' AND
        span_id ~ '^[0-9a-f]{16}$' AND
        (parent_span_id IS NULL OR parent_span_id ~ '^[0-9a-f]{16}$')
    );

ALTER TABLE agent_trace_spans DROP CONSTRAINT IF EXISTS agent_trace_spans_time_range_check;
ALTER TABLE agent_trace_spans ADD CONSTRAINT agent_trace_spans_time_range_check
    CHECK (ended_at IS NULL OR ended_at >= started_at);

ALTER TABLE agent_trace_spans DROP CONSTRAINT IF EXISTS agent_trace_spans_trace_span_key;
ALTER TABLE agent_trace_spans ADD CONSTRAINT agent_trace_spans_trace_span_key
    UNIQUE (trace_id, span_id);

ALTER TABLE agent_trace_spans DROP CONSTRAINT IF EXISTS agent_trace_spans_run_id_fkey;
ALTER TABLE agent_trace_spans ADD CONSTRAINT agent_trace_spans_run_id_fkey
    FOREIGN KEY (run_id) REFERENCES orchestration_runs (id) ON DELETE CASCADE;

ALTER TABLE agent_trace_spans DROP CONSTRAINT IF EXISTS agent_trace_spans_turn_id_fkey;
ALTER TABLE agent_trace_spans ADD CONSTRAINT agent_trace_spans_turn_id_fkey
    FOREIGN KEY (turn_id) REFERENCES agent_turns (id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_agent_trace_spans_run_started
    ON agent_trace_spans (run_id, started_at);

CREATE INDEX IF NOT EXISTS idx_agent_trace_spans_trace_started
    ON agent_trace_spans (trace_id, started_at);

CREATE INDEX IF NOT EXISTS idx_agent_trace_spans_expires_at
    ON agent_trace_spans (expires_at);

DROP TRIGGER IF EXISTS trg_agent_trace_spans_updated_at ON agent_trace_spans;
CREATE TRIGGER trg_agent_trace_spans_updated_at
    BEFORE UPDATE ON agent_trace_spans
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMIT;
