# Member C Database Schema Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the eight PostgreSQL-backed entities required for classroom multi-Agent orchestration, context compaction, shared memory, replayable SSE events, and local trace spans.

**Architecture:** Model each table as one focused GORM entity under `internal/model/entity`, register entities in dependency order in application startup, and place constraints that GORM cannot express in a separate idempotent SQL file. PostgreSQL remains the source of truth; no service or API behavior is added in this change.

**Tech Stack:** Go 1.24, GORM 1.31, PostgreSQL, standard `testing` package

**Spec:** `docs/member-c-database-design.md`

## Global Constraints

- Existing `migrations/0001_constraints.sql` is out of scope and must not be modified.
- Existing `.workbuddy/memory/2026-09-12.md` must remain untouched and untracked.
- Agent references use `classroom_agents.id`; no removed `classroom_agents.agent_key` field may be referenced.
- PostgreSQL is authoritative and Redis is not required by these entities.
- No raw prompts, chain of thought, secrets, or original Tool payload columns are introduced.
- `conversation_events` expire after 7 days by application policy; `agent_trace_spans` expire after 30 days by application policy.

---

### Task 1: Add schema contract tests

**Files:**
- Create: `internal/model/entity/member_c_schema_test.go`

**Interfaces:**
- Consumes: GORM entity structs through GORM's real `schema.Parse` behavior.
- Produces: A database-schema contract test for table names, database column names, JSONB declarations, and the absence of obsolete Agent columns.

- [x] **Step 1: Write the failing table-name and field tests**

Parse `ClassroomConversation`, `ConversationMessage`, `OrchestrationRun`, `AgentTurn`, `ContextCompaction`, `SharedContextMemory`, `ConversationEvent`, and `AgentTraceSpan` with `gorm.io/gorm/schema`. Assert the database table names and the critical persisted columns. Assert message and turn schemas contain `classroom_agent_id`, do not contain `agent_key`, and declare snapshot/payload/attributes columns as JSONB. Assert the append-only event schema has no `updated_at` column.

- [x] **Step 2: Run the tests and verify failure**

Run: `go test ./internal/model/entity`

Expected: compilation fails because the eight entity types do not exist.

### Task 2: Add conversation and context entities

**Files:**
- Create: `internal/model/entity/classroom_conversation.go`
- Create: `internal/model/entity/conversation_message.go`
- Create: `internal/model/entity/context_compaction.go`
- Create: `internal/model/entity/shared_context_memory.go`

**Interfaces:**
- Consumes: `entity.BaseModel`, `encoding/json`, and `time.Time`.
- Produces: `ClassroomConversation`, `ConversationMessage`, `ContextCompaction`, and `SharedContextMemory` with constants for all enum values and exact `TableName()` methods.

- [x] **Step 1: Implement the four entities from the spec**

Use `uint64` for bigint identifiers, pointer identifiers for nullable foreign keys, `json.RawMessage` for JSONB, `*time.Time` for nullable timestamps, and explicit GORM column/type/nullability tags.

- [x] **Step 2: Run entity tests**

Run: `go test ./internal/model/entity`

Expected: compilation still fails only for the four orchestration/event/trace types not yet created.

### Task 3: Add orchestration, event, and tracing entities

**Files:**
- Create: `internal/model/entity/orchestration_run.go`
- Create: `internal/model/entity/agent_turn.go`
- Create: `internal/model/entity/conversation_event.go`
- Create: `internal/model/entity/agent_trace_span.go`

**Interfaces:**
- Consumes: `entity.BaseModel`, `encoding/json`, and `time.Time`.
- Produces: `OrchestrationRun`, `AgentTurn`, `ConversationEvent`, and `AgentTraceSpan`, including enum constants used by future services.

- [x] **Step 1: Implement the four entities from the spec**

Do not embed `BaseModel` in `ConversationEvent`: it is append-only and has only `ID` and `CreatedAt`. Embed `BaseModel` in the other three entities because their statuses and end times are updated.

- [x] **Step 2: Run entity tests**

Run: `go test ./internal/model/entity`

Expected: PASS.

### Task 4: Register automatic table creation

**Files:**
- Modify: `internal/app/app.go`
- Test: `internal/app/app_migration_test.go`

**Interfaces:**
- Consumes: the eight new entity types.
- Produces: `databaseEntities() []any`, used by `AutoMigrate` and testable without opening PostgreSQL.

- [x] **Step 1: Write a failing migration-order test**

Add a test that calls `databaseEntities()` and asserts the complete order is existing base tables, then conversations/messages/compactions/runs/turns/memories/events/spans.

- [x] **Step 2: Extract and implement `databaseEntities`**

Replace the inline `AutoMigrate` argument list with `a.postgresDB.AutoMigrate(databaseEntities()...)`. Keep `PresetAgent` before `ClassroomAgent`, and place every referenced table before its dependent table.

- [x] **Step 3: Run app tests**

Run: `go test ./internal/app ./internal/model/entity`

Expected: PASS.

### Task 5: Add idempotent PostgreSQL constraints

**Files:**
- Create: `migrations/0003_member_c_constraints.sql`

**Interfaces:**
- Consumes: tables created by GORM AutoMigrate and existing `classrooms`, `scenes`, and `classroom_agents` tables.
- Produces: enum checks, uniqueness constraints, foreign keys, indexes, and `updated_at` triggers for all eight tables.

- [x] **Step 1: Implement the SQL constraints**

Use `DROP CONSTRAINT IF EXISTS` followed by `ADD CONSTRAINT` so rerunning the file is safe. Recreate `set_updated_at()` independently so this migration does not depend on the currently inconsistent `0001_constraints.sql`. Do not alter constraints on existing tables.

- [x] **Step 2: Validate SQL when local PostgreSQL tooling is available**

Check for `psql`. If a disposable local database configured for tests is available, apply the script twice in one clean schema to prove syntax and idempotency. Otherwise report that database execution validation was unavailable and rely on focused Go tests plus manual SQL review.

Result: `psql` is not installed and the local Docker service is not running, so database execution validation was unavailable.

- [x] **Step 3: Run focused Go tests**

Run: `go test ./internal/model/entity ./internal/app`

Expected: PASS.

### Task 6: Format and verify the full change

**Files:**
- Modify mechanically: all new Go files

**Interfaces:**
- Consumes: all prior tasks.
- Produces: formatted, compiling schema code with no unrelated modifications.

- [x] **Step 1: Format Go files**

Run: `gofmt -w internal/model/entity/classroom_conversation.go internal/model/entity/conversation_message.go internal/model/entity/context_compaction.go internal/model/entity/shared_context_memory.go internal/model/entity/orchestration_run.go internal/model/entity/agent_turn.go internal/model/entity/conversation_event.go internal/model/entity/agent_trace_span.go internal/model/entity/member_c_schema_test.go internal/app/app.go internal/app/app_migration_test.go`

- [x] **Step 2: Run all Go tests**

Run with a workspace-local `GOCACHE` and `GOTELEMETRY=off`: `go test ./...`

Expected: PASS.

- [x] **Step 3: Inspect scope**

Run: `git status --short` and `git diff --check`.

Expected: only the design, plan, eight entities, app migration registration, new constraint migration, and their tests are changed; `.workbuddy/memory/2026-09-12.md` remains untouched and untracked.
