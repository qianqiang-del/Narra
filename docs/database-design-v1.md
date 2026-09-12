# Narra V1 数据库设计（审核稿）

> 状态：待审核  
> 适用版本：本地单机部署 V1  
> 数据库：PostgreSQL 16+  
> 最后更新：2026-09-11

---

## 1. 设计目标与边界

Narra V1 是本地部署的个人/团队内使用版本。数据库只保存业务数据与运行记录；模型、搜索、语音等第三方服务的密钥均由配置文件或环境变量管理，不写入数据库。

### 1.1 V1 明确不做

- **不启用**登录、注册、鉴权、用户、角色、权限和多租户：不在任何路由上挂鉴权中间件，不校验任何身份。
- 不创建 `users`、`roles`、`permissions`、`api_keys` 等表。
- 不在数据库保存模型 API Key、数据库密码、TLS 私钥或其他密钥。
- 上下文、会话消息和记忆必须持久化到 PostgreSQL；Redis 仅用于加速读取、短期上下文缓存与实时任务状态，不能作为唯一存储。

关于鉴权代码：仓库中已存在 JWT 与鉴权中间件的骨架（`pkg/jwt/`、`internal/middleware/auth.go`）以及 `configs` 中的 `jwt` 配置段。这是**为后续迭代预留的骨架，V1 不启用**——当前无任何调用方，不挂载到路由，配置加载也不校验 `jwt.secret`。将来启用时需同时完成两件事：替换 `jwt.secret` 的占位值，并按 §9.1 创建用户表。

### 1.2 V1 要支持

- 创建、整理、删除和恢复本地课程。
- 一节课程有多个有序场景（讲解、测验、互动、项目实践、完成页）。
- 课程生成任务可以失败、重试和保留生成配置快照。
- Pro 工作台以课程为入口：每个会话必须归属一门课程；在会话中通过对话修改该课程的课件页，并可撤销最近一次修改。
- 工作台不提供联网搜索。素材上传、解析与 RAG 检索由其他模块负责，本设计只提供工作台会话作为素材归属的锚点。
- 为未来增加用户体系预留扩展路径，但 V1 的任意业务记录都不依赖用户表。

---

## 2. 总体关系

```text
folders
  1 ───── 0..N classrooms
                 │
                 ├───── 0..N scenes
                 │             │
                 │             ├───── 0..N scene_segments
                 │             └───── 0..1 scene_backups
                 ├───── 0..N classroom_agents
                 ├───── 0..N generation_jobs
                 └───── 1..N workbench_sessions
                                      │
                                      ├───── 0..N workbench_messages
                                      └───── 0..N session_memories
```

### 2.1 关系说明

- 文件夹可为空：未归档课程的 `classrooms.folder_id` 为 `NULL`。
- 课程是核心聚合根；场景、角色快照、讲解段落、生成任务和工作台会话均从属于课程。
- 工作台会话**必须**归属某一门课程，不存在无课程的会话；进入工作台首先选择课程，选定后默认开一个新会话，历史会话按课程分组查看。
- 工作台会话的产出是直接修改所属课程的场景，因此会话与 `scenes` 之间不设直接外键，通过 `classroom_id` 间接关联。
- `scene_backups` 每个场景最多保留一行，保存该场景最近一次被修改前的内容，用于撤销。

---

## 3. 通用约定

| 项目 | 约定 |
|---|---|
| 主键 | `bigint` + `GENERATED ALWAYS AS IDENTITY`（即 `bigserial` 语义）；Go 侧对应 `uint64` |
| 时间 | `timestamptz`，统一存 UTC |
| 删除策略 | 文件夹和课程硬删除；课程从属数据级联删除，含工作台会话及其下消息、记忆与备份 |
| 可变结构 | 使用 `jsonb`，但必须在应用层做版本与 schema 校验 |
| 文本编码 | UTF-8 |
| 命名 | 表和列使用 `snake_case`；枚举值使用小写英文 |
| 文件 | 数据库存元数据和本地相对路径，不保存文件二进制内容 |

需要软删除的主实体（`workbench_sessions`）具有：

```sql
created_at timestamptz NOT NULL DEFAULT now(),
updated_at timestamptz NOT NULL DEFAULT now(),
deleted_at timestamptz NULL
```

`updated_at` 由数据库触发器或 GORM hook 自动维护，最终实现时二选一并保持一致。

---

## 4. 实体设计

### 4.1 `folders`：课程文件夹

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | `bigint` | PK | 文件夹 ID |
| `name` | `varchar(120)` | 非空 | 文件夹名称 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 更新时间 |

规则：同一层级目前只有根目录，因此不需要 `parent_id`。删除文件夹时，所属课程的 `folder_id` 置为 `NULL`，课程不会被删除。

### 4.2 `classrooms`：课程主记录

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | `bigint` | PK | 课程 ID |
| `folder_id` | `bigint` | FK，可空 | 所属文件夹；删除文件夹时置空 |
| `title` | `varchar(200)` | 非空 | 课程名称 |
| `requirement` | `text` | 非空 | 用户原始生成需求 |
| `mode` | `varchar(32)` | 非空 | `vocational` 或 `interactive` |
| `status` | `varchar(32)` | 非空 | 课程当前状态，见 5.1 |
| `generation_config` | `jsonb` | 非空，默认 `{}` | 模型、搜索、解析器等本次生成快照 |
| `agent_config` | `jsonb` | 非空，默认 `{}` | 角色选择模式、自动生成策略与 TTS 配置 |
| `latest_generation_job_id` | `bigint` | 可空 | 最近一次生成任务，避免读取列表时聚合查询 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 更新时间 |

`generation_config` 推荐结构：

```json
{
  "provider_id": "openai",
  "model_id": "gpt-4o-mini",
  "web_search": true,
  "search_engine": "tavily",
  "extractor": "mineru",
  "deep_interactive": false
}
```

`agent_config` 推荐结构：

```json
{
  "mode": "preset",
  "selection_strategy": "manual",
  "tts_enabled": true
}
```

实际参与本课程的教师和其他角色，统一保存在 `classroom_agents`。`agent_config` 只保存本次采用的选择方式和生成策略，不重复存角色明细。

### 4.3 `classroom_agents`：课程角色快照

`preset` 表示用户手动选择代码中已有的角色；`auto` 表示系统按照生成策略从已有预设角色中自动选择，不代表大模型临时创建一套新的角色定义。无论哪种来源，写入课程后的 `name`、`role`、`role_type`、`system_prompt`、`voice_id`、`avatar` 和 `color` 都是本课程的冻结快照。

这张表保存一节课程最终实际使用的角色。它不是全局角色库；角色的默认定义、系统提示词模板和可用音色目录仍由代码/配置维护。课程生成时将最终使用的角色信息复制到本表，保证后续回放和重试不受默认配置变化影响。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | `bigint` | PK | 课程角色记录 ID |
| `classroom_id` | `bigint` | FK，非空 | 所属课程；级联删除 |
| `source` | `varchar(32)` | 非空 | `preset`（从代码预设角色中手动选择）或 `auto`（系统从预设角色中自动选择） |
| `agent_key` | `varchar(80)` | 非空 | 角色定义 ID，例如 `teacher`、`curious` |
| `name` | `varchar(120)` | 非空 | 角色展示名称 |
| `role` | `varchar(120)` | 非空 | 角色职责/定位，例如“主讲”“提问” |
| `role_type` | `varchar(32)` | 非空 | `teacher`、`assistant` 或 `student` |
| `system_prompt` | `text` | 非空 | 本课程实际使用的完整系统提示词快照；提示词模板来自代码/配置 |
| `voice_id` | `varchar(120)` | 非空 | 本课程实际使用的音色 ID |
| `avatar` | `varchar(255)` | 非空 | 六个默认头像之一的资源路径或 key |
| `color` | `varchar(16)` | 非空 | 角色界面主题色快照，例如 `#52c41a` |
| `sort_order` | `integer` | 非空，`>= 0` | 课堂中的角色顺序 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 更新时间 |

约束：

- `UNIQUE (classroom_id, sort_order)`，保证课堂角色顺序稳定。
- `UNIQUE (classroom_id, avatar)`，保证同一课程内六个默认头像不重复。
- 角色、提示词和音色写入后视为本课程快照；修改角色应创建新的生成版本，不覆盖历史记录。

示例：

```json
{
  "source": "auto",
  "agent_key": "curious",
  "name": "好奇宝宝",
  "role": "提问",
  "role_type": "student",
  "system_prompt": "你是课堂中的好奇提问者……",
  "voice_id": "voxcpm-zh-male-young",
  "avatar": "/avatars/curious-2.png",
  "color": "#52c41a"
}
```

### 4.4 `scenes`：课程场景/课件页

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | `bigint` | PK | 场景 ID |
| `classroom_id` | `bigint` | FK，非空 | 所属课程；级联删除 |
| `generation_job_id` | `bigint` | FK，可空 | 生成该场景的课程任务 |
| `sort_order` | `integer` | 非空，`>= 0` | 场景顺序，从 0 开始 |
| `type` | `varchar(32)` | 非空 | `slide`、`quiz`、`interactive`、`pbl`、`complete` |
| `title` | `varchar(200)` | 非空 | 场景标题 |
| `content_status` | `varchar(32)` | 非空 | 页面 JSON 生成状态，见 5.2 |
| `narration_status` | `varchar(32)` | 非空 | 老师讲解段落生成状态，见 5.2 |
| `content` | `jsonb` | 非空，默认 `{}` | 按场景类型保存的内容，统一包含可渲染的 `blocks` 数组 |
| `content_version` | `smallint` | 非空，默认 `1` | 场景 JSON schema 版本，用于未来结构迁移 |
| `error_message` | `text` | 可空 | 本场景生成失败的错误摘要 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 更新时间 |

约束：`UNIQUE (classroom_id, sort_order)`。

`content` 与前端场景渲染模型一致。每个可展示或可讲解的 block 必须有同一场景内唯一的稳定 `key`：

```json
{
  "blocks": [
    { "key": "intro-variable", "type": "paragraph", "content": "变量可以理解为一个带名字的盒子" },
    { "key": "example-code", "type": "code", "content": "name = \"Narra\"" }
  ]
}
```

约束：同一场景内 `key` 不得重复；`scene_segments.content_key` 必须能找到对应 block；大模型只生成结构化 JSON，不直接生成 HTML；前端负责将 key 渲染为 `data-content-key`；不同 `type` 的 JSON schema 由后端领域层校验。

### 4.5 `scene_segments`：场景讲解段落

这张表保存老师对场景内容的逐段讲解。它不是单纯的讲稿列表：每条记录必须通过稳定的 `content_key` 对应到 `scenes.content` 中的一个具体内容块，前端再将该 key 写入 HTML 元素的 `data-content-key` 属性，用于高亮、滚动、逐段播放和 TTS。

本表不重复保存 `content_type`。内容块类型统一由 `scenes.content.blocks[].type` 提供，前端通过 `content_key` 找到对应 block 后读取其类型，避免数据库字段与场景 JSON 类型不一致。

页面内容和讲解段落的关系如下：

```text
scenes.content
  ├── {"key":"paragraph-1", "type":"paragraph"} → scene_segments.content_key = paragraph-1
  ├── {"key":"paragraph-2", "type":"paragraph"} → scene_segments.content_key = paragraph-2
  └── {"key":"code-1", "type":"code"}             → scene_segments.content_key = code-1
```

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | `bigint` | PK | 讲解段落 ID |
| `scene_id` | `bigint` | FK，非空 | 所属场景；级联删除 |
| `speaker_classroom_agent_id` | `bigint` | FK，非空 | 发言角色；通常关联本课程的教师角色 |
| `content_key` | `varchar(120)` | 非空 | 对应场景 JSON 内容块的稳定 key |
| `sort_order` | `integer` | 非空，`>= 0` | 讲解播放顺序 |
| `text` | `text` | 非空 | 老师实际讲解的文本 |
| `status` | `varchar(32)` | 非空 | `pending`、`generating`、`ready`、`failed` |
| `audio_path` | `text` | 可空 | 后续 TTS 音频文件相对路径 |
| `duration_ms` | `integer` | 可空，`>= 0` | 音频时长 |
| `error_message` | `text` | 可空 | 讲解或 TTS 失败摘要 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 更新时间 |

约束：

- `UNIQUE (scene_id, content_key)`，同一场景内一个内容块只对应一个讲解段落。
- `UNIQUE (scene_id, sort_order)`，保证播放顺序稳定。
- `speaker_classroom_agent_id` 必须属于同一课程；V1 默认由教师角色讲解，但保留角色外键以支持未来多角色讲解。
- `content_key` 必须能在所属场景 JSON 的 `blocks` 中找到；内容块的 `type` 只从 JSON 读取。
- `audio_path` 和 `duration_ms` V1 可为空，等待 TTS 模块生成。

前端渲染约定：

```html
<p data-content-key="paragraph-1">变量可以理解为一个带名字的盒子。</p>
```

播放第 N 段时，前端根据 `content_key` 查找对应 DOM 元素，执行高亮、滚动和播放同步。不能只依赖数组下标，因为用户编辑或重新排序场景内容后，下标可能发生变化。

教师音色不在本表重复保存，而是从 `classroom_agents.voice_id` 读取。课程角色表中的教师记录确定本节课最终使用的音色，讲解段落只保存发言角色引用。

### 4.6 `generation_jobs`：课程生成任务

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | `bigint` | PK | 任务 ID |
| `classroom_id` | `bigint` | FK，非空 | 所属课程；级联删除 |
| `parent_job_id` | `bigint` | FK，可空 | 重试任务指向原任务 |
| `kind` | `varchar(32)` | 非空 | `classroom_generation` 或 `regenerate_scene` |
| `status` | `varchar(32)` | 非空 | 任务状态，见 5.3 |
| `current_scene_id` | `bigint` | FK，可空 | 当前正在生成的场景；生成大纲阶段为空 |
| `current_phase` | `varchar(32)` | 可空 | `outline`、`scene_content`、`scene_narration`、`completed` |
| `request_config` | `jsonb` | 非空，默认 `{}` | 此次任务的完整输入快照 |
| `result_summary` | `jsonb` | 非空，默认 `{}` | 场景数、token 用量、模型响应 ID 等摘要 |
| `error_code` | `varchar(64)` | 可空 | 业务错误码 |
| `error_message` | `text` | 可空 | 失败摘要，禁止写入密钥 |
| `started_at` | `timestamptz` | 可空 | 开始时间 |
| `finished_at` | `timestamptz` | 可空 | 结束时间 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 更新时间 |

一个课程同一时间只允许一个运行中的生成任务。此规则由事务/应用锁保证；如需数据库强约束，可增加针对 `status IN ('queued', 'running')` 的部分唯一索引。

### 4.7 `workbench_sessions`：工作台会话

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | `bigint` | PK | 会话 ID |
| `classroom_id` | `bigint` | FK，**非空** | 所属课程；课程删除时级联删除 |
| `title` | `varchar(200)` | 非空 | 会话标题 |
| `status` | `varchar(32)` | 非空 | `active`、`archived` |
| `agent_config` | `jsonb` | 非空，默认 `{}` | 工作台 Agent 配置快照 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 更新时间 |
| `deleted_at` | `timestamptz` | 可空 | 软删除时间 |

规则：

- 会话**必须**归属一门课程，`classroom_id` 不可为空；课程删除时会话级联删除，不保留孤儿会话。
- 会话在用户发出**第一条消息**时才落库。进入课程后未发言就离开，不会产生空会话。
- 历史会话按课程分组查询，条件为 `classroom_id`，配合下方索引。
- 工作台不提供联网搜索。可用能力只有上传素材解析与预置 skill，其中素材的存储与解析由素材模块负责（见第 6 节），本设计不建素材表。skill 是代码中的常量集合（例如「精简讲解」「补充练习」），**不建表**，仅在 `agent_config` 中记录本次使用的 skill 标识。

### 4.8 `workbench_messages`：工作台消息

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | `bigint` | PK | 消息 ID |
| `session_id` | `bigint` | FK，非空 | 所属会话；级联删除 |
| `sequence` | `integer` | 非空，`> 0` | 会话内严格递增的消息序号 |
| `role` | `varchar(32)` | 非空 | `system`、`user`、`assistant`、`tool` |
| `content` | `text` | 非空，默认空字符串 | 文本内容 |
| `content_format` | `varchar(32)` | 非空，默认 `markdown` | `markdown`、`plain_text`、`json` |
| `status` | `varchar(32)` | 非空，默认 `complete` | `streaming`、`complete`、`failed` |
| `metadata` | `jsonb` | 非空，默认 `{}` | 模型、token、引用来源等 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 更新时间 |

约束：`UNIQUE (session_id, sequence)`。

### 4.9 `session_memories`：会话持久化记忆

会话上下文和摘要以 PostgreSQL 为准，Redis 只缓存最近窗口。即使 Redis 被清空或过期，也可以从本表和 `workbench_messages` 恢复上下文。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | `bigint` | PK | 记忆 ID |
| `session_id` | `bigint` | FK，非空 | 所属会话；级联删除 |
| `kind` | `varchar(32)` | 非空 | `summary`、`fact`、`preference` |
| `content` | `text` | 非空 | 摘要或记忆内容 |
| `metadata` | `jsonb` | 非空，默认 `{}` | 重要性、来源等 |
| `message_until_sequence` | `integer` | 可空 | 此摘要已覆盖到的最后消息序号 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 更新时间 |

### 4.10 `scene_backups`：课件修改前备份

保存场景**最近一次被修改前**的内容，用于撤销工作台对课件的一次修改。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `scene_id` | `bigint` | PK，FK | 所属场景；级联删除 |
| `batch_id` | `bigint` | 非空 | 修改批次标识；同一次模型修改涉及的所有场景共享同一个值 |
| `title` | `varchar(200)` | 非空 | 修改前的场景标题 |
| `content` | `jsonb` | 非空 | 修改前的场景内容 |
| `created_at` | `timestamptz` | 非空 | 备份时间 |

索引：`(batch_id)`。

规则：

- 每个场景**最多保留一行**，即只支持撤销最近一次修改；再次修改会覆盖上一份备份，不保留更早版本。
- 一次工作台修改**会**同时涉及多个场景——用户一次可要求改多页，且即使工具是单场景粒度，一条用户消息也可能触发多次调用。`batch_id` 把它们归为一组：撤销时整批还原，符合用户「撤销刚才那次修改」的心智，而不是逐页撤销。
- 撤销的语义是「把该 `batch_id` 对应批次的场景内容写回 `scenes` 并更新其 `updated_at`」，备份行本身保留。
- `scene_id` 同时作为主键，天然保证一个场景只有一份备份。
- `batch_id` 不是行标识，**不走 `IDENTITY`**：直接复用触发本次修改的那条**用户消息**的 `workbench_messages.id`。它本身已是一个 `bigint`，无需额外生成器；且天然保证「一条用户消息 → 一个批次」——即使 Agent 分几轮调用工具，只要源自同一条用户消息就归为同一批，符合用户对「刚才那次修改」的感知。
- 只备份 `title` 与 `content`；`sort_order`、`type` 等结构字段不在工作台的修改范围内。

### 4.11 工具调用：V1 不建表

工作台与课堂 Agent 的工具调用**不进入业务数据库**，改用结构化日志：

```go
logger.Info("tool_call",
    zap.String("tool", toolID),
    zap.Duration("duration", d),
    zap.String("session_id", sessionID),
    zap.String("status", status),
)
```

理由：

- 原 `tool_calls` 表的字段与链路追踪系统中的一根 span 一一对应（`call_id`→span_id、`tool_id`→span name、`request`/`response`→attributes、`duration_ms`→时长、`error_code`→error 状态），其用途是排错，属于可观测性范畴而非业务数据。
- V1 工具集只有「素材解析」与「预置 skill」两项，调用量小，`pkg/logger`（zap + lumberjack）已覆盖排错需求。
- V1 无 worker 租约与重试机制，不需要 `call_id` 提供的幂等去重。
- 若独立成表并挂业务外键 `ON DELETE CASCADE`，课程删除会连带删除排错证据，与可观测性「数据应比被观测对象活得更久」的要求相悖。

后续若需要查询、聚合或看板，再引入 OpenTelemetry SDK 与追踪后端（Jaeger / Langfuse），异步导出，不写入业务库。

---

## 5. 状态值

### 5.1 课程状态 `classrooms.status`

| 状态 | 含义 |
|---|---|
| `draft` | 已创建，尚未开始生成 |
| `outlining` | 正在生成课程大纲 |
| `generating` | 正在逐场景生成内容 |
| `ready` | 所有场景及其讲解均已生成，课程可播放 |
| `failed` | 最近生成流程失败 |

### 5.2 场景内容与讲解状态

`scenes` 将页面内容和老师讲解拆成两个独立状态。这样可以准确表达“页面已经生成，但讲解还在生成”的中间状态。

#### `scenes.content_status`

| 状态 | 含义 |
|---|---|
| `pending` | 已有大纲，等待生成页面 JSON |
| `generating` | 正在生成页面 JSON |
| `ready` | 页面 JSON 已生成，可供前端渲染 |
| `failed` | 生成失败，可重试 |

#### `scenes.narration_status`

| 状态 | 含义 |
|---|---|
| `pending` | 页面 JSON 已生成，等待生成讲解段落 |
| `generating` | 正在生成老师讲解段落 |
| `ready` | 所有讲解段落均已生成 |
| `failed` | 讲解生成失败，可重试 |

场景是否可以正常播放由两个状态共同决定：

```text
content_status = ready
AND narration_status = ready
→ 场景可完整播放
```

`complete` 不再作为场景状态；课程完成页仍通过 `scenes.type = complete` 表示。

### 5.3 任务状态 `generation_jobs.status`

| 状态 | 含义 |
|---|---|
| `queued` | 已创建，等待执行 |
| `running` | 正在执行 |
| `succeeded` | 成功完成 |
| `failed` | 失败 |
| `cancelled` | 用户取消 |

初版采用 `varchar + CHECK` 而不是 PostgreSQL 原生 ENUM，后续增加状态时迁移成本更低。

---

## 6. 外键与删除规则

| 子表/字段 | 父表 | 删除动作 |
|---|---|---|
| `classrooms.folder_id` | `folders.id` | `SET NULL` |
| 删除 `classrooms` | 课程从属数据 | `CASCADE` 删除场景、课程角色、生成任务、工作台会话及会话下的消息与记忆 |
| `scenes.classroom_id` | `classrooms.id` | `CASCADE` |
| `scenes` 被删除 | `scene_backups` | `CASCADE` |
| `generation_jobs.classroom_id` | `classrooms.id` | `CASCADE` |
| `workbench_sessions.classroom_id` | `classrooms.id` | `CASCADE` |
| `workbench_messages.session_id` | `workbench_sessions.id` | `CASCADE` |
| `session_memories.session_id` | `workbench_sessions.id` | `CASCADE` |

课程删除立即级联删除场景、课程角色、生成任务，以及工作台会话及其下全部数据。工作台会话不允许脱离课程存在，因此 `workbench_sessions.classroom_id` 采用 `CASCADE` 而非 `SET NULL`。

素材上传、解析、向量化与 RAG 检索由**其他模块**负责，不在本数据库设计中建表。本设计只提供 `workbench_sessions` 作为素材归属的锚点：素材模块的表通过 `session_id` 外键指向 `workbench_sessions.id`，其自身的删除规则由该模块决定。

课程封面不单独保存图片路径，前端使用该课程 `sort_order = 0` 的首个场景 JSON 渲染封面。

---

## 7. 索引设计

| 表 | 索引 | 目的 |
|---|---|---|
| `folders` | `(created_at DESC) WHERE deleted_at IS NULL` | 文件夹列表 |
| `classrooms` | `(folder_id, updated_at DESC)` | 文件夹内课程列表 |
| `classrooms` | `(updated_at DESC)` | 最近课程 |
| `scenes` | `UNIQUE(classroom_id, sort_order)` | 场景顺序与读取 |
| `generation_jobs` | `(classroom_id, created_at DESC)` | 查看课程生成历史 |
| `workbench_sessions` | `(classroom_id, updated_at DESC) WHERE deleted_at IS NULL` | 某课程下的历史会话列表 |
| `workbench_messages` | `UNIQUE(session_id, sequence)` | 会话顺序读取 |
| `scene_backups` | `(batch_id)` | 按修改批次整批撤销 |

V1 暂不对大段文本建全文索引；当工作台历史搜索成为真实需求后，再为 `workbench_messages.content` 加 PostgreSQL FTS。素材解析、向量化和 RAG 检索由其他模块单独设计。

---

## 8. 建表迁移顺序

第一批迁移（课程主链路）：

1. 创建通用 `updated_at` 触发器（若采用触发器方案）。
2. 创建 `folders`。
3. 创建 `classrooms`。
4. 创建 `generation_jobs`。
5. 创建 `scenes`。
6. 创建 `scene_segments`。
7. 添加 `classrooms.latest_generation_job_id` 的延迟外键。

第二批迁移（工作台与 Agent）：

1. 创建 `workbench_sessions`。
2. 创建 `workbench_messages`。
3. 创建 `session_memories`。
4. 创建 `scene_backups`（依赖第一批的 `scenes`）。

工作台消息和会话记忆写入 PostgreSQL 成功后，再更新 Redis 缓存；Redis 写入失败不能回滚已经提交的数据库记录。

这样能先完成“创建课程 → 大纲 → 场景 → 课程播放”的闭环，再接入工作台与 MCP。

---

## 9. 未来扩展（不进入 V1 迁移）

### 9.1 增加用户体系

后续创建 `users` 后，可在 `folders`、`classrooms`、`workbench_sessions` 添加可空 `owner_id`，完成历史数据迁移后再改为非空。V1 不应预先放一个无意义的 `user_id` 或固定“本地用户”。

代码侧的对应动作：启用 §1.1 中保留的 JWT 与鉴权中间件骨架，替换 `jwt.secret` 占位值，并把 `Auth()` 挂到需要保护的路由组上。因此本次迭代只需新增表和迁移，不必从零搭建鉴权基础设施。

### 9.2 多层文件夹

若需要目录树，再向 `folders` 加 `parent_id bigint NULL REFERENCES folders(id)`；当前单层文件夹足以匹配现有前端。

### 9.3 Agent 版本管理

当人设可被界面编辑或需要版本回放时，再新增 `agent_profiles` 与 `agent_profile_versions`；目前使用 `agent_config` 快照即可。

### 9.4 向量与 RAG

RAG 文档切片、embedding 模型版本及向量索引由知识管线模块单独设计。不要把它们混入本 V1 业务主库的第一批迁移。

---

## 10. 审核结论

以下 12 项已全部确认，作为编写 SQL migration 与 GORM Model 的前提。

**数据库与范围**

1. **PostgreSQL 为 V1 唯一关系型数据库。** Redis 只作缓存与实时状态，不作唯一存储（见 §1.1）；不引入第二种关系型数据库。
2. **V1 无任何用户表、登录表和鉴权持久化表。** 不创建 `users`、`roles`、`permissions`、`api_keys` 等表；业务记录不依赖用户表。未来扩展路径见 §9.1。
3. **素材上传、解析与 RAG 检索由其他模块负责。** 本设计只提供 `workbench_sessions.id` 作为素材归属锚点，不在本库建素材表，其删除规则由该模块决定（见 §6）。
4. **主键统一使用 `bigint` + `GENERATED ALWAYS AS IDENTITY`（Go 侧 `uint64`），不使用 UUID。** 因此也不需要 `pgcrypto`（见 §3、§8）。

**删除规则**

5. **文件夹硬删除，下辖课程保留。** 删除文件夹只删除文件夹行本身；该文件夹下所有课程的 `classrooms.folder_id` 置为 `NULL`，课程变为未归档状态，**不会被删除**（见 §4.1、§6）。
6. **课程硬删除，从属数据级联删除。** 删除课程时，其场景、课程角色、生成任务、工作台会话及会话下的消息与记忆一并级联删除（见 §6）。

**工作台**

7. **工作台会话必须归属课程。** `classroom_id` 非空、`CASCADE`，不存在任何无课程的会话；进入工作台先选课程，会话在发出第一条消息时才落库（见 §4.7、§6）。
8. **一次工作台修改会同时涉及多个场景，`scene_backups.batch_id` 保留。** 撤销按批次整批还原，不是逐场景撤销。`batch_id` 复用触发本次修改的**用户消息**的 `workbench_messages.id`，不走 `IDENTITY`（见 §4.10）。
9. **V1 工具调用只写结构化日志，不建 `tool_calls` 表**（见 §4.11）。

**课程与角色**

10. **课程实际角色统一写入 `classroom_agents`。** 包括教师、手动预设角色和自动模式最终选中的角色，并保存提示词、音色、头像、颜色快照（见 §4.3）。

**实施节奏**

11. **保留 JWT 与鉴权中间件骨架，但 V1 不启用。** `pkg/jwt/`、`internal/middleware/auth.go` 与 `configs` 中的 `jwt` 配置段保留，作为后续迭代的骨架；当前零调用、不挂路由（见 §1.1）。
12. **工作台相关的四张表本轮只设计、不立即建表。** `workbench_sessions`、`workbench_messages`、`session_memories`、`scene_backups` 本轮只出设计，迁移脚本在后续迭代生成。

---

本文档的 V1 数据库设计到此定稿。后续按 §8 的迁移顺序编写 SQL migration 与 GORM Model。
