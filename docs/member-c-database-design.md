# 成员 C 数据库设计：多 Agent 编排、上下文、共享记忆与链路追踪

> 状态：待团队评审
> 日期：2026-09-14
> 范围：成员 C「项目核心」模块
> 部署形态：用户本地部署，单用户使用

## 1. 设计目标

本设计覆盖四项职责：

1. 多 Agent 编排：记录一次用户输入怎样被 Director 分派给一个或多个 Agent。
2. 对话上下文管理：保存课堂对话，并在上下文超限时生成可追溯的摘要。
3. 跨成员上下文记忆：让同一课堂中的所有 Agent 共享事实、决定和学习状态。
4. 链路追踪：在本地数据库中还原 Director、Agent、模型、Tool、记忆和 SSE 的执行链路。

本设计只负责课堂内的多 Agent 对话，不负责成员 A 的 Pro 工作台单 Agent 会话和工作台记忆。

## 2. 依赖的现有表

本模块直接沿用以下现有表，不复制角色资料：

- `classrooms`：课程主记录。
- `scenes`：课程中的课件页。
- `classroom_agents`：某门课程实际选择的角色、提示词、头像、类型和课程音色快照。

Agent 身份统一使用 `classroom_agents.id`。需要角色名称、人设、类型、头像或主题色时，通过
`classroom_agents` 当前课程角色快照读取。

由于 `classroom_agents` 保存当前课程角色集合，历史消息和回合必须另外保存角色快照。否则课程更换
角色后，旧消息会丢失显示身份。

## 3. 通用约定

- 数据库继续使用项目现有的 PostgreSQL，不单独改为 SQLite。
- 主键使用 `bigint GENERATED ALWAYS AS IDENTITY`。
- 时间使用 `timestamptz`，统一保存 UTC。
- 可扩展结构使用 `jsonb`，默认 `{}`。
- 普通业务表包含 `created_at`、`updated_at`；追加式事件只包含 `created_at`。
- 本地单用户版本不增加 `user_id`、`tenant_id` 或组织字段。
- PostgreSQL 是事实来源；Redis 只能作为可选缓存，Redis 不可用时核心功能仍须正常运行。
- 错误字段只保存脱敏摘要，禁止保存密钥、完整模型提示词、原始 Tool 响应和模型隐藏思维过程。

## 4. 总体关系

```text
classrooms
└── classroom_conversations
    ├── conversation_messages
    ├── context_compactions
    ├── shared_context_memories
    ├── conversation_events
    └── orchestration_runs
        ├── agent_turns
        └── agent_trace_spans

agent_turns.classroom_agent_id
    -> classroom_agents.id
```

本模块共设计 8 张表。

## 5. 表结构

### 5.1 `classroom_conversations`：课堂对话

一门课程可以有多个话题。问答、讨论和针对某一页的讲解分别保存为独立对话。

| 字段 | 类型 | 空值/默认 | 说明 |
|---|---|---|---|
| `id` | `bigint` | 主键 | 对话 ID |
| `classroom_id` | `bigint` | 非空 | 所属课程 |
| `origin_scene_id` | `bigint` | 可空 | 发起对话时所在课件页 |
| `title` | `varchar(200)` | 非空 | 对话标题 |
| `type` | `varchar(32)` | 非空 | `qa`、`discussion`、`lecture` |
| `status` | `varchar(32)` | 非空，默认 `active` | `active`、`closed` |
| `last_message_at` | `timestamptz` | 可空 | 最后一条消息时间，用于列表排序 |
| `ended_at` | `timestamptz` | 可空 | 对话关闭时间 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 更新时间 |

约束与索引：

- `classroom_id -> classrooms.id ON DELETE CASCADE`。
- `origin_scene_id -> scenes.id ON DELETE SET NULL`。
- `CHECK (type IN ('qa', 'discussion', 'lecture'))`。
- `CHECK (status IN ('active', 'closed'))`。
- 索引 `(classroom_id, last_message_at DESC)`。
- 应用层校验 `origin_scene_id` 必须属于同一个 `classroom_id`。

### 5.2 `conversation_messages`：对话消息

只保存用户实际发送的消息、Agent 最终发言和系统可见通知。Director 决策和 Tool 调用不伪装成聊天
消息，分别进入回合表和追踪表。

| 字段 | 类型 | 空值/默认 | 说明 |
|---|---|---|---|
| `id` | `bigint` | 主键 | 消息 ID |
| `conversation_id` | `bigint` | 非空 | 所属课堂对话 |
| `sequence_no` | `bigint` | 非空 | 对话内从 1 开始的严格递增序号 |
| `sender_type` | `varchar(16)` | 非空 | `user`、`agent`、`system` |
| `classroom_agent_id` | `bigint` | 可空 | Agent 消息的发送角色 |
| `sender_snapshot` | `jsonb` | 非空，默认 `{}` | 发言时角色的展示快照 |
| `content` | `text` | 非空 | 完整消息正文 |
| `status` | `varchar(32)` | 非空 | `streaming`、`completed`、`failed`、`cancelled` |
| `reply_to_message_id` | `bigint` | 可空 | 被回复的消息 |
| `token_count` | `integer` | 非空，默认 0 | 消息 Token 数 |
| `metadata` | `jsonb` | 非空，默认 `{}` | 引用、附件标识等扩展信息 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 流式生成状态更新时间 |

`sender_snapshot` 推荐结构：

```json
{
  "agent_key": "curious",
  "name": "好奇宝宝",
  "role_type": "student",
  "avatar": "/avatars/curious.png",
  "color": "#1677ff"
}
```

约束与索引：

- `conversation_id -> classroom_conversations.id ON DELETE CASCADE`。
- `classroom_agent_id -> classroom_agents.id ON DELETE SET NULL`。
- `reply_to_message_id -> conversation_messages.id ON DELETE SET NULL`。
- `UNIQUE (conversation_id, sequence_no)`。
- `CHECK (sequence_no >= 1)`、`CHECK (token_count >= 0)`。
- `CHECK (sender_type IN ('user', 'agent', 'system'))`。
- `CHECK (status IN ('streaming', 'completed', 'failed', 'cancelled'))`。
- Agent 消息写入时必须有 `classroom_agent_id` 和非空角色快照；外键之后被置空不影响历史显示。

### 5.3 `orchestration_runs`：编排运行

一条用户消息触发一次 Run；同一条消息重新执行时递增 `attempt_no`，不覆盖旧执行记录。

| 字段 | 类型 | 空值/默认 | 说明 |
|---|---|---|---|
| `id` | `bigint` | 主键 | Run ID |
| `conversation_id` | `bigint` | 非空 | 所属课堂对话 |
| `trigger_message_id` | `bigint` | 非空 | 触发本次编排的用户消息 |
| `attempt_no` | `smallint` | 非空，默认 1 | 本消息第几次执行 |
| `trace_id` | `varchar(32)` | 非空 | 全链路追踪 ID，使用 32 位十六进制 |
| `status` | `varchar(32)` | 非空 | Run 状态 |
| `max_turns` | `smallint` | 非空 | 最大 Agent 回合数 |
| `stop_reason` | `varchar(32)` | 可空 | 停止原因 |
| `orchestrator_version` | `varchar(40)` | 非空 | 编排器版本，便于回放与排查 |
| `config_snapshot` | `jsonb` | 非空，默认 `{}` | 模型和编排参数快照 |
| `started_at` | `timestamptz` | 可空 | 开始执行时间 |
| `finished_at` | `timestamptz` | 可空 | 结束时间 |
| `error_message` | `text` | 可空 | 脱敏错误摘要 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 状态更新时间 |

状态取值：

```text
queued -> running -> waiting_user | completed | failed | cancelled
```

停止原因取值：`completed`、`waiting_user`、`max_turns`、`error`、`cancelled`。

约束与索引：

- `conversation_id -> classroom_conversations.id ON DELETE CASCADE`。
- `trigger_message_id -> conversation_messages.id`。
- `UNIQUE (trigger_message_id, attempt_no)`、`UNIQUE (trace_id)`。
- `CHECK (attempt_no >= 1)`、`CHECK (max_turns BETWEEN 1 AND 50)`。
- 索引 `(conversation_id, created_at DESC)`。
- 应用层校验触发消息属于同一对话且 `sender_type = 'user'`。

### 5.4 `agent_turns`：Agent 回合

Director 每选择一次具体 Agent，就创建一条 Turn。一个 Run 可以按顺序包含多个 Turn。

| 字段 | 类型 | 空值/默认 | 说明 |
|---|---|---|---|
| `id` | `bigint` | 主键 | Turn ID |
| `run_id` | `bigint` | 非空 | 所属编排 Run |
| `turn_no` | `smallint` | 非空 | Run 内从 1 开始的顺序 |
| `classroom_agent_id` | `bigint` | 可空 | 被选中的课程角色 |
| `agent_snapshot` | `jsonb` | 非空 | 执行当时的角色快照 |
| `output_message_id` | `bigint` | 可空 | 最终产生的可见消息 |
| `status` | `varchar(32)` | 非空 | 回合状态 |
| `selection_reason` | `text` | 可空 | Director 选择该角色的简短、可审计原因 |
| `next_action` | `varchar(32)` | 可空 | 回合结束后的动作 |
| `model` | `varchar(120)` | 可空 | 本回合使用的模型 |
| `input_tokens` | `integer` | 非空，默认 0 | 输入 Token 数 |
| `output_tokens` | `integer` | 非空，默认 0 | 输出 Token 数 |
| `started_at` | `timestamptz` | 可空 | 开始时间 |
| `finished_at` | `timestamptz` | 可空 | 结束时间 |
| `error_message` | `text` | 可空 | 脱敏错误摘要 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 状态更新时间 |

状态为 `scheduled`、`running`、`completed`、`failed`、`cancelled`；`next_action` 为
`continue`、`switch_agent`、`ask_user`、`end`。

约束与索引：

- `run_id -> orchestration_runs.id ON DELETE CASCADE`。
- `classroom_agent_id -> classroom_agents.id ON DELETE SET NULL`。
- `output_message_id -> conversation_messages.id ON DELETE SET NULL`。
- `UNIQUE (run_id, turn_no)`、`UNIQUE (output_message_id)`。
- Token 数不得小于 0，`turn_no >= 1`。
- 应用层校验角色属于 Run 对应的课程，输出消息属于 Run 对应的对话。

`selection_reason` 只保存“该角色适合回答当前问题”这类结果性说明，不保存模型隐藏思维过程。

### 5.5 `context_compactions`：上下文压缩

摘要采用累积版本：新摘要可以吸收上一版摘要和后续消息，并通过 `previous_compaction_id` 保留版本链。

| 字段 | 类型 | 空值/默认 | 说明 |
|---|---|---|---|
| `id` | `bigint` | 主键 | 摘要 ID |
| `conversation_id` | `bigint` | 非空 | 所属对话 |
| `previous_compaction_id` | `bigint` | 可空 | 上一版摘要 |
| `covered_from_sequence` | `bigint` | 非空 | 覆盖起始消息序号 |
| `covered_to_sequence` | `bigint` | 非空 | 覆盖结束消息序号 |
| `summary` | `text` | 非空 | 可直接加入模型上下文的摘要 |
| `key_points` | `jsonb` | 非空，默认 `{}` | 事实、决定和未解决问题 |
| `source_tokens` | `integer` | 非空 | 压缩前 Token 数 |
| `summary_tokens` | `integer` | 非空 | 压缩后 Token 数 |
| `model` | `varchar(120)` | 可空 | 摘要模型 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 修正时间 |

约束与索引：

- `conversation_id -> classroom_conversations.id ON DELETE CASCADE`。
- `previous_compaction_id -> context_compactions.id ON DELETE SET NULL`。
- `UNIQUE (conversation_id, covered_to_sequence)`。
- `CHECK (covered_from_sequence >= 1)`。
- `CHECK (covered_to_sequence >= covered_from_sequence)`。
- Token 数不得小于 0，且成功摘要应小于 `source_tokens`。
- 索引 `(conversation_id, covered_to_sequence DESC)`。

上下文读取顺序固定为：系统提示词、最新摘要、摘要之后的完整消息、共享记忆、当前用户消息。

### 5.6 `shared_context_memories`：共享上下文记忆

本表不设置 `owner_agent_id`。所有课堂 Agent 查询同一份有效记忆，从数据结构上保证“跨成员共享”。

| 字段 | 类型 | 空值/默认 | 说明 |
|---|---|---|---|
| `id` | `bigint` | 主键 | 记忆 ID |
| `classroom_id` | `bigint` | 非空 | 所属课程 |
| `conversation_id` | `bigint` | 可空 | 对话级记忆所属对话 |
| `scope` | `varchar(16)` | 非空 | `conversation`、`classroom` |
| `memory_type` | `varchar(32)` | 非空 | 记忆类型 |
| `content` | `text` | 非空 | 记忆正文 |
| `importance` | `smallint` | 非空，默认 3 | 重要程度 1 至 5 |
| `source_message_id` | `bigint` | 可空 | 来源消息 |
| `source_turn_id` | `bigint` | 可空 | 来源 Agent 回合 |
| `status` | `varchar(16)` | 非空，默认 `active` | `active`、`superseded`、`retracted` |
| `superseded_by_id` | `bigint` | 可空 | 替代本记忆的新记录 |
| `expires_at` | `timestamptz` | 可空 | 临时记忆过期时间 |
| `last_used_at` | `timestamptz` | 可空 | 最近加入上下文的时间 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 更新时间 |

`memory_type` 为 `fact`、`decision`、`learning_state`、`preference`、`open_question`。

约束与索引：

- `classroom_id -> classrooms.id ON DELETE CASCADE`。
- `conversation_id -> classroom_conversations.id ON DELETE CASCADE`。
- 来源消息、来源回合和替代记录删除时置空。
- `CHECK (importance BETWEEN 1 AND 5)`。
- `scope = 'classroom'` 时 `conversation_id IS NULL`。
- `scope = 'conversation'` 时 `conversation_id IS NOT NULL`。
- 索引 `(classroom_id, scope, status, importance DESC)`。
- 索引 `(conversation_id, status, importance DESC)`。
- 应用层校验对话、来源消息和来源回合都属于同一个课堂上下文。

课堂级记忆可跨同一课程的多个对话使用；对话级记忆只在当前话题内使用。

### 5.7 `conversation_events`：SSE 事件与断线续传

该表先由成员 C 定义并写入，成员 B 的 SSE 层按 `conversation_id + sequence_no` 查询和推送。成员 B
后续可以调整事件名称及 `payload` 内容，但不改变顺序和重放机制。

| 字段 | 类型 | 空值/默认 | 说明 |
|---|---|---|---|
| `id` | `bigint` | 主键 | 事件 ID |
| `conversation_id` | `bigint` | 非空 | 所属课堂对话 |
| `run_id` | `bigint` | 可空 | 关联 Run |
| `turn_id` | `bigint` | 可空 | 关联 Turn |
| `sequence_no` | `bigint` | 非空 | 对话内从 1 开始的事件序号 |
| `event_type` | `varchar(64)` | 非空 | 事件类型 |
| `payload` | `jsonb` | 非空，默认 `{}` | 前端消费的事件载荷 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `expires_at` | `timestamptz` | 非空 | 默认创建后 7 天 |

初始事件类型：

```text
run.started
director.decision
agent.started
message.delta
message.completed
agent.completed
run.waiting_user
run.completed
run.failed
```

约束与索引：

- 对话删除时事件级联删除；Run 或 Turn 单独删除时对应外键置空。
- `UNIQUE (conversation_id, sequence_no)`。
- `CHECK (sequence_no >= 1)`。
- 索引 `(expires_at)`，供本地清理任务使用。

SSE 的 `id` 使用 `sequence_no`。客户端重连时提交最后收到的序号，服务端返回该对话中序号更大的
事件。`message.delta` 每约 100 毫秒或达到一定字符数合并写入一批，不按单 Token 插入。最终完整正文
只以 `conversation_messages` 为准，事件过期不会导致历史消息丢失。

`sequence_no` 必须在数据库事务中分配。单机仍可能有多个 Go goroutine 同时写事件，不能使用进程内
普通整数直接递增；应锁定对应对话行或使用等价的数据库原子分配方式。

### 5.8 `agent_trace_spans`：本地链路追踪

本地版不强制安装 Jaeger、Tempo 或 Langfuse。追踪记录直接保存到 PostgreSQL，同时采用兼容
OpenTelemetry 的 Trace ID 和 Span ID，未来可以无损接入外部追踪系统。

| 字段 | 类型 | 空值/默认 | 说明 |
|---|---|---|---|
| `id` | `bigint` | 主键 | 内部记录 ID |
| `trace_id` | `varchar(32)` | 非空 | 整次编排链路 ID |
| `span_id` | `varchar(16)` | 非空 | 当前步骤 ID |
| `parent_span_id` | `varchar(16)` | 可空 | 父步骤 ID |
| `run_id` | `bigint` | 非空 | 所属 Run |
| `turn_id` | `bigint` | 可空 | 所属 Agent 回合 |
| `kind` | `varchar(32)` | 非空 | 步骤种类 |
| `name` | `varchar(120)` | 非空 | 步骤名称 |
| `status` | `varchar(16)` | 非空 | `running`、`ok`、`error`、`cancelled` |
| `started_at` | `timestamptz` | 非空 | 开始时间 |
| `ended_at` | `timestamptz` | 可空 | 结束时间 |
| `attributes` | `jsonb` | 非空，默认 `{}` | 模型、Tool、Token、重试次数等 |
| `input_summary` | `text` | 可空 | 脱敏输入摘要 |
| `output_summary` | `text` | 可空 | 脱敏输出摘要 |
| `error_message` | `text` | 可空 | 脱敏错误摘要 |
| `expires_at` | `timestamptz` | 非空 | 默认创建后 30 天 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | Span 结束时更新时间 |

`kind` 为 `orchestration`、`director`、`agent`、`model`、`tool`、`memory`、`sse`。

约束与索引：

- `run_id -> orchestration_runs.id ON DELETE CASCADE`。
- `turn_id -> agent_turns.id ON DELETE SET NULL`。
- `UNIQUE (trace_id, span_id)`。
- `CHECK (ended_at IS NULL OR ended_at >= started_at)`。
- 索引 `(run_id, started_at)`、`(trace_id, started_at)`、`(expires_at)`。

## 6. 核心数据流程

```text
用户发送消息
  -> 写 conversation_messages
  -> 创建 orchestration_runs
  -> 写 run.started 事件和根 Span
  -> Director 选择 Agent
  -> 创建 agent_turns
  -> Agent 读取摘要、最近消息、共享记忆
  -> 模型或 Tool 执行并写子 Span
  -> 分批写 message.delta 事件
  -> 完成后写 conversation_messages 最终正文
  -> 更新 agent_turns 和 orchestration_runs
  -> 提取或更新 shared_context_memories
  -> 必要时生成 context_compactions
  -> 写完成事件并关闭 Span
```

失败时保留已经成功写入的消息和回合。当前步骤写 `failed` 与脱敏错误，Run 根据是否还能继续决定继续
选择其他 Agent，或进入 `failed`。达到 `max_turns` 必须结束，避免 Agent 无限对话。

## 7. 删除与保留规则

| 操作 | 处理 |
|---|---|
| 删除课程 | 对话、消息、Run、Turn、摘要、记忆、事件和追踪全部级联删除 |
| 删除单个对话 | 删除该对话的全部从属数据；课堂级记忆保留 |
| 课程更换角色 | 历史外键置空，依靠消息和回合中的角色快照继续显示 |
| SSE 事件到期 | 默认 7 天后清理；不影响最终消息 |
| Trace 到期 | 默认 30 天后清理；不影响 Run 和 Turn 业务记录 |
| Redis 数据丢失 | 从 PostgreSQL 重建上下文，不能导致消息或记忆丢失 |

清理任务在应用启动后及每天运行一次即可。保留天数应提供本地配置项，允许用户关闭自动清理或修改
期限。

## 8. 建表依赖顺序

后续实现迁移时按以下顺序创建，可避免循环外键：

1. `classroom_conversations`
2. `conversation_messages`
3. `context_compactions`
4. `orchestration_runs`
5. `agent_turns`
6. `shared_context_memories`
7. `conversation_events`
8. `agent_trace_spans`

`agent_turns.output_message_id` 等后置引用可以在相关表创建完成后再添加外键。

## 9. 不在本设计中的内容

- 成员 A 的 `workbench_sessions`、`workbench_messages` 和工作台记忆。
- 成员 B 的文件上传、RAG 文档、切块和向量表。
- 登录、用户、组织和云端多租户。
- 模型隐藏思维过程、完整提示词和原始敏感调用载荷。
- 现有角色实体与旧迁移脚本之间的不一致，本设计暂不处理。

## 10. 评审结论

这 8 张表可以完整覆盖成员 C 的四项职责，并适合本地部署：关键数据只有 PostgreSQL 一个事实来源，
Redis 和外部可观测服务均不是运行前提。设计中需要跨表验证“消息、场景和角色属于同一课程”的规则
由应用层事务保证，因为现有表没有提供可直接复用的复合外键。

成员 B 后续只需要评审 `conversation_events.event_type`、`payload` 和重连参数；即使调整这些接口，
也不会改变其余 7 张表的结构。
