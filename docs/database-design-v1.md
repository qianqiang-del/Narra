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
- 课程生成在后台进行：用户可中途离开，回来按课程状态继续查看；首页场景的内容与讲解都生成完毕即可进入课堂，其余场景继续生成。生成失败时整门课程置为失败并保留原因，用户回首页重新发起。
- Pro 工作台以课程为入口：每个会话必须归属一门课程；在会话中通过对话修改该课程的课件页。V1 不提供撤销。
- 工作台 Agent 可调用 `web_search`（与课堂 Agent 是同一个工具）和 `doc_extract` 两个工具。素材上传、解析与 RAG 检索由其他模块负责，本设计只提供工作台会话作为素材归属的锚点。
- 为未来增加用户体系预留扩展路径，但 V1 的任意业务记录都不依赖用户表。

---

## 2. 总体关系

```text
folders
  1 ───── 0..N classrooms
                 │
                 ├───── 0..N scenes
                 │             │
                 │             └───── 0..N scene_segments
                 ├───── 0..N classroom_agents
                 ├───── 0..N classroom_memories
                 └───── 1..N workbench_sessions
                                      │
                                      └───── 0..N workbench_messages
```

### 2.1 关系说明

- 文件夹可为空：未归档课程的 `classrooms.folder_id` 为 `NULL`。
- 课程是核心聚合根；场景、角色快照、讲解段落、课程记忆和工作台会话均从属于课程。
- 工作台会话**必须**归属某一门课程，不存在无课程的会话；进入工作台首先选择课程，选定后默认开一个新会话，历史会话按课程分组查看。
- 工作台会话的产出是直接修改所属课程的场景，因此会话与 `scenes` 之间不设直接外键，通过 `classroom_id` 间接关联。

---

## 3. 通用约定

| 项目 | 约定 |
|---|---|
| 主键 | `bigint` + `GENERATED ALWAYS AS IDENTITY`（即 `bigserial` 语义）；Go 侧对应 `uint64` |
| 时间 | `timestamptz`，统一存 UTC |
| 删除策略 | **全部硬删除，无软删除**；文件夹和课程直接删除，课程从属数据级联删除，含工作台会话及其下消息，以及课程记忆 |
| 可变结构 | 使用 `jsonb`，但必须在应用层做版本与 schema 校验 |
| 文本编码 | UTF-8 |
| 命名 | 表和列使用 `snake_case`；枚举值使用小写英文 |
| 文件 | 数据库存元数据和本地相对路径，不保存文件二进制内容 |

所有表统一具有：

```sql
created_at timestamptz NOT NULL DEFAULT now(),
updated_at timestamptz NOT NULL DEFAULT now()
```

`updated_at` 由数据库触发器或 GORM hook 自动维护，最终实现时二选一并保持一致。

**V1 不使用软删除。** 业务实体一律硬删除，删除的连带影响交给 `CASCADE`（§6）。会话删除是硬删除，其下的消息随之级联删除；因此没有任何表带 `deleted_at`，查询也不需要过滤已删除行。

**文本列的长度是硬约束，不是提示。** PostgreSQL 的 `varchar(n)` 超长是**报错**，不是静默截断——一次超长会回滚整个事务，把同一批已经写好的数据一起丢掉。因此凡是可能由大模型产出或来自用户自由输入的 `varchar` 列，写入前必须在应用层**按字符**（不是按字节）截断到上限以内；数据库的长度约束只负责拦下漏网的脏数据。目前属于这一类的是 `classroom_agents.name` / `persona`、`scenes.title`、`scene_segments.content_key`、`workbench_sessions.title`。

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
| `mode` | `varchar(32)` | 非空，CHECK | `vocational` 或 `interactive` |
| `status` | `varchar(32)` | 非空，CHECK | 课程当前状态，见 5.1 |
| `generation_error` | `text` | 可空 | 整条生成流程中断的原因；仅 `status = 'failed'` 时有值 |
| `generation_config` | `jsonb` | 非空，默认 `{}` | 模型、搜索、解析器等本次生成快照 |
| `agent_config` | `jsonb` | 非空，默认 `{}` | 角色选择模式、自动生成策略与 TTS 配置 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 更新时间 |

约束：

- `CHECK (mode IN ('vocational', 'interactive'))`。
- `CHECK (status IN ('draft', 'outlining', 'generating', 'playable', 'ready', 'failed'))`，与 §5.1 一一对应；增减状态值时必须同时改这里。

生成状态直接记在本表上，**没有独立的生成任务表**。V1 一次生成只跑一趟：不并发、不重试、不取消、不支持断点续传，因此「这门课生成到哪了」就是 `status`、「为什么停了」就是 `generation_error`。将来若开放「对同一门课重新生成」，才需要单独的表记录每一趟任务。

`generation_error` 与 `scenes.error_message` 分工不同：后者记的是「这一页为什么没生成出来」，记的时候整条流程还在往下跑；前者记的是「整个流程为什么停了」。大纲阶段挂掉时一条场景都还没建，`scenes` 表里空无一物，这一列是唯一的失败记录。两者同样只写能给人看的摘要，禁止写入原始 API 响应、堆栈和密钥，落库前按字符截断。

`generation_config` 推荐结构：

```json
{
  "provider_id": "openai",
  "model_id": "gpt-4o-mini",
  "web_search": true,
  "search_engine": "tavily",
  "extractor": "mineru"
}
```

`agent_config` 推荐结构：

```json
{
  "mode": "preset"
}
```

`mode` 只表示**槽位由谁选定**：`preset` 是用户自己勾选，`auto` 是系统从预设槽位里挑。实际参与本课程的教师和其他角色统一保存在 `classroom_agents`，`agent_config` 不重复存角色明细。

`agent_config` 不放 `selection_strategy`（与 `mode` 是同一个意思）也不放 `tts_enabled`（V1 讲解恒有音频，没有关闭入口），等真有开关再加。

### 4.3 `classroom_agents`：课程角色快照

槽位固定为 **6 个**：教师 `teacher`，以及 5 个预设学生 `assist`、`clown`、`curious`、`note-taker`、`thinker`。`agent_key` 始终是这 6 个之一，永不为空。

`preset` 表示用户手动选择代码中已有的角色；`auto` 表示系统从这 6 个槽位中自动选择，并由**大模型重写名称、人设与提示词**。无论哪种来源，大模型都不创建新的角色定义：`role`、`role_type`、`color` 由后端按槽位从代码预设取出，`avatar` 与 `voice_id` 在 `auto` 下由大模型从那 6 套预设值中挑选，且必须经应用层校验。写入课程后的 `name`、`persona`、`role`、`role_type`、`system_prompt`、`voice_id`、`avatar` 和 `color` 都是本课程的冻结快照。

这张表保存一节课程最终实际使用的角色。它不是全局角色库；角色的默认定义、系统提示词模板和可用音色目录仍由代码/配置维护。课程生成时将最终使用的角色信息复制到本表，保证后续回放和重试不受默认配置变化影响。

生成时哪些字段会变：

| 字段 | `preset` | `auto` |
|---|---|---|
| `agent_key` | 用户勾选的槽位 | 系统从 6 个槽位中选定 |
| `name` / `persona` / `system_prompt` | 代码预设里的值 | 大模型重写（纯文本） |
| `avatar` / `voice_id` | 跟随槽位 | 大模型从那 6 套预设值中挑选，应用层校验兜底 |
| `role` / `role_type` / `color` | 跟随槽位 | 跟随槽位，**不由大模型产出** |

`auto` 模式下由大模型产出的字段（`name`、`persona`、`system_prompt`、`avatar`、`voice_id`）必须在**应用层**校验后再落库。数据库的长度约束与 `CHECK` 是最后一道防线，只负责拦下脏数据，不负责修正：

- `name` 按**字符**截断到 120，`persona` 截断到 255。PostgreSQL 的 `varchar` 超长是**报错**而非静默截断，会让整个生成事务回滚。
- `name` 为空或纯空白时回退为预设名称；清掉换行、首尾空白与控制字符。
- `persona` 是给人看的卡片正文，它必须与 `name` 一致——名字改了、简介不能还是原来那句。
- `system_prompt` 虽为 `text`，也应在应用层设长度上限，避免回放性能与 token 成本失控。
- `avatar` 与 `voice_id` 由大模型在预设值中挑选，但大模型可能返回不存在的值（编造一个资源路径或音色 ID）。数据库只看得到长度，拦不住这种错，因此必须在应用层拿返回值与预设清单比对：对得上就用，对不上则回退为槽位原配的那一套。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | `bigint` | PK | 课程角色记录 ID |
| `classroom_id` | `bigint` | FK，非空 | 所属课程；级联删除 |
| `source` | `varchar(32)` | 非空，CHECK | 名称与提示词的来源：`preset`（代码预设，**教师恒为本值**）或 `auto`（大模型按课程内容重写） |
| `agent_key` | `varchar(80)` | 非空 | 槽位定义 ID，恒为 `teacher`、`assist`、`clown`、`curious`、`note-taker`、`thinker` 之一 |
| `name` | `varchar(120)` | 非空 | 角色展示名称；`preset` 沿用预设，`auto` 由大模型重写，写入前须按字符截断 |
| `role` | `varchar(120)` | 非空 | 角色职责/定位，例如“主讲”“提问”；跟随槽位固定 |
| `role_type` | `varchar(32)` | 非空，CHECK | `teacher`、`assistant` 或 `student`；由后端按槽位从代码预设取出，**不由大模型产出** |
| `persona` | `varchar(255)` | 非空 | 角色人设简介，展示在角色信息卡正文；`preset` 来自代码/配置，`auto` 由大模型随 `name` 一起重写，写入前须按字符截断 |
| `system_prompt` | `text` | 非空 | 本课程实际使用的完整系统提示词快照；`preset` 来自代码/配置，`auto` 由大模型重写 |
| `voice_id` | `varchar(120)` | 非空 | 音色 ID；`preset` 跟随槽位，`auto` 由大模型从预设值中挑选，写入前须经应用层校验 |
| `avatar` | `varchar(255)` | 非空 | 六个默认头像之一的资源路径或 key；`preset` 跟随槽位，`auto` 由大模型从预设值中挑选，写入前须经应用层校验 |
| `color` | `varchar(16)` | 非空 | 角色界面主题色快照，例如 `#52c41a`；跟随槽位固定 |
| `sort_order` | `integer` | 可空，`>= 0` | 学员之间的排列位次，从 0 开始按预设槽位顺序确定，用户不可调整；教师不属于该序列，恒为 `NULL` |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 更新时间 |

约束：

- `UNIQUE (classroom_id, sort_order)`，保证学员顺序稳定。`sort_order` 是学员之间的位次，从 0 开始；教师不参与该序列（恒为 `NULL`），PostgreSQL 视多个 NULL 互不相等，因此不影响这条约束。
- `UNIQUE (classroom_id, agent_key)`，保证同一预设角色在一门课内只出现一次，同时保证**教师唯一**（`agent_key = 'teacher'` 只可能有一行）。唯一约束建在 `agent_key` 而不是 `avatar` 上：`avatar` 只是展示属性（文件名带 `-2` 后缀，会换图），`agent_key` 才是角色身份。
- `CHECK (source IN ('preset', 'auto'))`、`CHECK (role_type IN ('teacher', 'assistant', 'student'))`。
- `source` 记的是名称与提示词的来源，不是"谁选的槽位"，因此不能与 `classrooms.agent_config.mode` 合并：`auto` 模式下 `mode = 'auto'`，而教师行的 `source` 仍是 `preset`。
- 教师恒有且仅有一行，课程创建时写入，用户只能改它的音色；它不参与大模型重写，`source` 恒为 `preset`，也不占用 `sort_order`。学生行可以为 0 到 5 行（用户一个都不勾就是 0 行），因此一门课的 `classroom_agents` 最少 1 行、最多 6 行。
- 本表只保存这门课当前正在使用的角色，不保留历史版本：教师行自课程创建起一直存在，用户改音色即更新这一行；学生行按本次选定的角色集合写入，换了一批角色重新生成时，旧行删除、新行插入。大模型生成的只是「这门课用哪些角色、叫什么、什么人设」，改不到代码里那 6 个预设定义——历史角色仍然是历史角色。讲解只由教师发声，学生行不会被 `scene_segments` 引用，因此删除旧行不会打断任何讲解段落。

示例：

```json
{
  "source": "auto",
  "agent_key": "curious",
  "name": "爱较真的小周",
  "role": "提问",
  "role_type": "student",
  "persona": "爱抠细节的那一个，总在别人觉得理所当然的地方停下来问一句「凭什么」。",
  "system_prompt": "你是课堂中的好奇提问者，本节围绕矩阵乘法展开……",
  "voice_id": "voxcpm-zh-male-young",
  "avatar": "/avatars/curious-2.png",
  "color": "#52c41a",
  "sort_order": 2
}
```

### 4.4 `scenes`：课程场景/课件页

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | `bigint` | PK | 场景 ID |
| `classroom_id` | `bigint` | FK，非空 | 所属课程；级联删除 |
| `sort_order` | `integer` | 非空，`>= 0` | 场景顺序，从 0 开始 |
| `type` | `varchar(32)` | 非空，CHECK | `slide`、`quiz`、`interactive`、`pbl`、`complete` |
| `title` | `varchar(200)` | 非空 | 场景标题；由大模型产出，写入前须按字符截断 |
| `content_status` | `varchar(32)` | 非空，CHECK | 页面 JSON 生成状态，见 5.2 |
| `narration_status` | `varchar(32)` | 非空，CHECK | 老师讲解段落生成状态，见 5.2 |
| `content` | `jsonb` | 非空，默认 `{}` | 场景内容，统一为 `{"blocks":[...]}`，前端按每个 block 的 `type` 渲染 |
| `error_message` | `text` | 可空 | 本场景生成失败的错误摘要，写入规则见下 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 更新时间 |

约束：

- `UNIQUE (classroom_id, sort_order)`。
- `CHECK (type IN ('slide', 'quiz', 'interactive', 'pbl', 'complete'))`。
- `CHECK (content_status IN ('pending', 'generating', 'ready', 'failed'))`、`CHECK (narration_status IN ('pending', 'generating', 'ready', 'failed'))`，与 §5.2 一一对应。

`content` 与前端场景渲染模型一致，统一为 `blocks` 数组，前端按**每个 block 的 `type`** 选择渲染方式：

| block `type` | 渲染成 |
|---|---|
| `heading` | 标题 |
| `paragraph` | 段落正文 |
| `list-item` | 列表项；渲染时把连续的 `list-item` 合并成一个列表，由自身的 `ordered` 标志决定 `<ul>` 还是 `<ol>` |
| `callout` | 强调块（装饰性短语） |
| `code` | 代码块 |
| `quiz` | 选择题（题干、选项、正确选项下标） |
| `browser` | 伪浏览器演示（地址栏、标题、说明） |
| `columns` | 分栏；栏内条目同样是独立 block |

这份清单是前端渲染的契约，增删都要前后端一起改；每种 `type` 的 JSON schema 由后端领域层校验。

**block 是最小的可高亮单位。** `scene_segments.content_key` 永远指向一个具体 block，不支持 `list.2` 这类子路径：列表的每个要点、分栏里的每个条目都各占一个 block。老师这一段讲的是第 2 个要点，就指向第 2 个 `list-item` 自己的 `key`——前端同步逻辑因此只有一条路径，不必判断"这个 key 指的是整块还是块里的第几项"。

`scenes.type` **不参与渲染**，只是场景的分类标签，用处有三：生成大纲时按它排课（`interactive` 模式下会排入更多交互演示页）、侧栏按它分类上色、课程完成页靠 `type = 'complete'` 识别。

每个可展示或可讲解的 block 必须有同一场景内唯一的稳定 `key`：

```json
{
  "blocks": [
    { "key": "intro-variable", "type": "paragraph", "content": "变量可以理解为一个带名字的盒子" },
    { "key": "example-code", "type": "code", "content": "name = \"Narra\"" }
  ]
}
```

约束：同一场景内 `key` 不得重复；`scene_segments.content_key` 必须能找到对应 block；大模型只生成结构化 JSON，不直接生成 HTML；前端负责将 key 渲染为 `data-content-key`。

**`error_message` 的写入规则。** 一页失败不中断整条流程——跳过它，继续生成后面的场景。所以「这一页为什么没出得来」只有这里记；课程级的失败原因在 `classrooms.generation_error`（§4.2），两者不重叠。也正因为它是唯一记录，容易被人图省事把原始报错整个塞进去，所以要定三条：

- 只写**能给人看的摘要**，例如“模型返回的内容超出长度限制”。
- **禁止写入原始 API 响应、堆栈和密钥**（与 `classrooms.generation_error` 同一条规矩）。
- 落库前按**字符**截断（建议 500）。类型用 `text` 而非 `varchar`，正好避开 `varchar` 超长报错、把整个生成事务回滚的坑。

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
| `content_key` | `varchar(120)` | 非空 | 对应场景 JSON 内容块的稳定 key；由大模型产出，写入前须按字符截断 |
| `sort_order` | `integer` | 非空，`>= 0` | 讲解播放顺序 |
| `text` | `text` | 非空 | 老师实际讲解的文本 |
| `status` | `varchar(32)` | 非空，CHECK | `pending`、`generating`、`ready`、`failed`；`ready` 表示讲稿与音频都已就绪，这一段可以播 |
| `audio_path` | `text` | 可空 | TTS 音频文件相对路径；`ready` 时必须非空 |
| `error_message` | `text` | 可空 | 讲解或 TTS 失败摘要 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 更新时间 |

约束：

- `UNIQUE (scene_id, content_key)`，同一场景内一个内容块只对应一个讲解段落。
- `UNIQUE (scene_id, sort_order)`，保证播放顺序稳定。
- `speaker_classroom_agent_id` 必须属于同一课程；V1 默认由教师角色讲解，但保留角色外键以支持未来多角色讲解。
- `content_key` 必须能在所属场景 JSON 的 `blocks` 中找到；内容块的 `type` 只从 JSON 读取。
- `CHECK (status IN ('pending', 'generating', 'ready', 'failed'))`。
- `CHECK (status <> 'ready' OR audio_path IS NOT NULL)`：`ready` 的含义就是「这一段能播了」，所以必须已经有音频；`pending`、`generating`、`failed` 下允许为空。音频时长不落库，前端从音频元素自身读（`HTMLAudioElement.duration`）。

**`status` 覆盖讲稿与 TTS 两关，但只有一个值**，所以失败时要靠 `text` 区分是哪一关挂的：

- `text` 非空 + `failed` → 讲稿已经写出来了，挂的是 TTS 合成。重试只需重做音频，不用重写讲稿。
- `text` 为空 + `failed` → 讲稿本身没生成出来，整段重做。

`text` 是 `not null`，讲稿还没生成时存空串而不是 `NULL`——这是上面这条判断能成立的前提。

前端渲染约定：

```html
<p data-content-key="paragraph-1">变量可以理解为一个带名字的盒子。</p>
```

播放第 N 段时，前端根据 `content_key` 查找对应 DOM 元素，执行高亮、滚动和播放同步。不能只依赖数组下标，因为用户编辑或重新排序场景内容后，下标可能发生变化。

教师音色不在本表重复保存，而是从 `classroom_agents.voice_id` 读取。课程角色表中的教师记录确定本节课最终使用的音色，讲解段落只保存发言角色引用。

### 4.6 `workbench_sessions`：工作台会话

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | `bigint` | PK | 会话 ID |
| `classroom_id` | `bigint` | FK，**非空** | 所属课程；课程删除时级联删除 |
| `title` | `varchar(200)` | 非空 | 会话标题；写入前须按字符截断 |
| `summary` | `text` | 可空 | 滚动摘要：把已覆盖的历史消息压成的一段话；为空表示还没有摘要 |
| `summary_until_sequence` | `integer` | 可空 | 摘要已覆盖到的最后一条消息序号；与 `summary` 同时为空或同时有值 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 更新时间 |

规则：

- 会话**必须**归属一门课程，`classroom_id` 不可为空；课程删除时会话级联删除，不保留孤儿会话。
- 会话在用户发出**第一条消息**时才落库。进入课程后未发言就离开，不会产生空会话。
- **删除会话是硬删除**，其下的 `workbench_messages` 随 `CASCADE` 一并删除（§6）。V1 不提供归档，也不提供恢复——一个会话要么在，要么没了。因此本表没有 `status`，也没有 `deleted_at`。
- 历史会话按课程分组查询，条件为 `classroom_id`，配合 §7 的索引。
- 工作台 Agent 可调用的工具是 `web_search` 与 `doc_extract`（`web_search` 与课堂 Agent 用的是同一个），外加用于写入课程记忆的 `remember`（§4.8）。其中素材的存储与解析由素材模块负责（见第 6 节），本设计不建素材表。
- skill 是代码中的常量集合（例如「精简讲解」「补充练习」），**不建表**。工作台前端目前没有加载 skill 的入口（i18n 里那句 `loadSkill` 属于已删除页面的遗留，无组件引用），而且就算要做，skill 是「这一轮加载」的消息级信息，会话级的字段也记不住它。
- **本表没有 `agent_config`。** 工作台 Agent 该有的配置项都不归会话管：模型由课程决定——同一门课自始至终用一个模型，存在 `classrooms.generation_config` 的 `provider_id` / `model_id`（§4.2）；skill 见上一条。
- **`summary` 是滚动摘要，一个会话始终只有这一份。** 会话聊长、上下文快塞满时，把「已有摘要 + 这一批新消息」一起交给大模型重新压一遍，覆盖写回。不保留历史版本，也不是「压一批插一行」——后者恢复时要把多条摘要拼起来，内容还互相重叠。
- 摘要**落库**的理由不是怕 Redis 丢，而是**重跑的结果不稳定**：大模型是概率的，同一批消息压两次得到的两段话不一样，上下文一变 Agent 的行为就漂移。所以压出来一次就存下来。
- `summary_until_sequence` 是「还有哪些消息没被摘要覆盖」的判据：拼上下文时只送 `sequence > summary_until_sequence` 的原始消息，再加上这份摘要。为空表示还没压过，全部消息照常发送。

### 4.7 `workbench_messages`：工作台消息

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | `bigint` | PK | 消息 ID |
| `session_id` | `bigint` | FK，非空 | 所属会话；级联删除 |
| `sequence` | `integer` | 非空，`> 0` | 会话内严格递增的消息序号 |
| `role` | `varchar(32)` | 非空，CHECK | `user`、`assistant` |
| `content` | `text` | 非空，默认空字符串 | 文本内容 |
| `content_format` | `varchar(32)` | 非空，默认 `markdown`，CHECK | `markdown`、`plain_text` |
| `status` | `varchar(32)` | 非空，默认 `complete`，CHECK | `complete`、`failed` |
| `metadata` | `jsonb` | 非空，默认 `{}` | 预留扩展位，V1 无消费方 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 更新时间 |

约束：

- `UNIQUE (session_id, sequence)`。
- `CHECK (role IN ('user', 'assistant'))`、`CHECK (content_format IN ('markdown', 'plain_text'))`、`CHECK (status IN ('complete', 'failed'))`。

规则：

- **本表只存给人看的对话，`role` 只有 `user` 和 `assistant` 两种。** 工具调用不进本表：`web_search`、`doc_extract` 的调用与结果由链路追踪模块采集（§4.9）。这样表里留下的就是一份干净的对话记录。
- **assistant 消息落库前必须把 `tool_calls` 剥掉，`content` 只存纯文本。** 不能存成 `{"text": "...", "tool_calls": [...]}` 这种混合体——`content_format` 已经声明是 `markdown` / `plain_text`，存进去的就得真的是那个格式。
- **模型「先输出空文本 + `tool_calls`、等工具结果回来再说话」的中间态不落库**，只落最终那条有文本的 assistant 消息，否则表里会堆一串 `content = ''` 的空行。
- 由此付出的代价是：Agent 拼上下文时看不到自己上一轮调过什么。这里认了——工具结果的权威副本在别处（解析结果在素材模块、课件改动在 `scenes`），而且几万字的工具结果进了上下文照样会被滚动摘要压掉（§4.6）。
- **写入时机：成功落一条 `complete`，失败落一条 `failed`，不写 `streaming` 中间态。** 流式过程中不落库，吐完了才插，所以每条回复只写一次库。不采用「先插空行、边流边更新」的写法——要么每个 token 都写一次库，要么断线时库里留半句残话，两种都不划算。也正因为不产生 `streaming` 行，就没有「崩溃后残留的半截消息」需要清理。
- **失败时 `content` 存已经吐出来的那部分**，前端刷新后仍能显示「生成失败，点击重试」。不落这条的话界面上会一片空白，用户会以为自己的消息没发出去。
- **进程崩溃时这次回复在库里没有任何记录**，用户重问一次即可。
- **`metadata` 是预留的扩展位，写入时留空对象 `{}`。** 设计意图是放模型、token、引用来源这类附加信息，但 V1 前端不展示模型名、不显示 token 用量、也没有引用 UI，所以**暂时没有消费方**。保留它是为了以后往里加 key 时不必改表结构（`jsonb` 的 key 不需要 migration）。

### 4.8 `classroom_memories`：课程长期记忆

本表只装**课程长期记忆**：用户偏好、课程设定这类跨会话还要用的信息，一条一句。它**不是对话摘要**——摘要是会话级的单一属性，存在 `workbench_sessions.summary`（见 §4.6）。

**归属课程而不是会话。** 一门课下会有多条会话（§4.6），记忆要跨会话生效；挂在会话上的话，用户在会话 A 里说的偏好，新开一个会话 B 就看不到了。查记忆直接按 `classroom_id` 取，不需要 join `workbench_sessions`；用户删掉一个旧会话，也不会连带删掉这门课的偏好。

因此本表不需要再用一个字段标注记忆种类：能进本表的就代表「跨会话还要用」。会话级的东西不进来——原话在 `workbench_messages`，聊过的浓缩在 `workbench_sessions.summary`。

会话上下文以 PostgreSQL 为准，Redis 只缓存最近窗口。即使 Redis 被清空或过期，也可以从 `workbench_messages`（原始消息）和 `workbench_sessions.summary`（滚动摘要）恢复上下文。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | `bigint` | PK | 记忆 ID |
| `classroom_id` | `bigint` | FK，非空 | 所属课程；级联删除 |
| `content` | `text` | 非空 | 记忆内容，自包含、不带指代的一整句话 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 更新时间 |

#### 写入规则

**由谁写。** 工作台 Agent 通过一个 `remember` 工具主动写入。代码不自动抽取（不为了记忆每轮额外跑一次大模型），也不依赖用户手动点击。

**判断标准只有一条：换个会话，这句话还成立吗。** 成立才写。

| 用户说的 | 下个会话还成立吗 | 进哪 |
|---|---|---|
| "这门课讲解口语化一点" | 成立 | 进本表 |
| "面向初二学生" | 成立 | 进本表 |
| "别用太多动画" | 成立 | 进本表 |
| "第3页加个例子" | 不成立，只对这次改动有意义 | 不进，归 `workbench_messages` |
| "刚才那页重做一下" | 不成立 | 不进，归 `workbench_messages` |

判断由大模型在工具描述（提示词）的约束下完成——"换了会话还成不成立"是语义判断，代码兜底不了。提示词里必须写明三件事：

1. 附上正反例，即上表。
2. **明确说"大多数轮次什么都不该记"**——大模型有讨好倾向，不压住它会每轮都往里塞。
3. 要求改写成自包含、不带指代的句子："改成口语化" → "这门课的讲解改成口语化"。否则过后翻出来不知道改的是什么。

**不做去重。** 同一条规矩说两遍会写两行，交给 Agent 自己看着办（它拼上下文时本来就能看到全部记忆）。V1 不加去重逻辑。

#### 读取规则

拼上下文的顺序是：**本表记忆 + `workbench_sessions.summary` + `summary_until_sequence` 之后的原始消息**。

送多少条：`ORDER BY id DESC LIMIT 50`。**这只是保险丝**——一门课实际能定几条长期规矩？"口语化"、"面向初二"、"别太多动画"，5 到 15 条顶天了，到不了 50。真到了 50 条，说明提示词写坏了，该去修提示词。

因此**表里不设上限、不做删除**，`LIMIT` 只加在查询上——比"写入时删最旧的"少一段逻辑，还不会误删。触发上限时被挤掉的是最早记的那批，而"这门课面向初二学生"恰恰是最早记、最该一直带着的，所以更不能让它成为常规路径。

V1 不做记忆管理界面。若日后滥记成灾，再加一个设置页把记忆列出来让用户删。

### 4.9 工具调用：V1 不建表

工作台与课堂 Agent 的工具调用**不进入业务数据库**：调用与结果由**链路追踪模块**统一采集（该模块的可观测设计不在本文件范围），本设计只在工具调用处埋点。业务库这一侧什么都不存——`workbench_messages` 里也不会出现工具消息（§4.7）。

V1 需要埋点的工具：

| 工具 | 谁用 | 说明 |
|---|---|---|
| `web_search` | 课堂 Agent、工作台 Agent | 网络搜索，两边调的是同一个工具 |
| `doc_extract` | 课堂 Agent、工作台 Agent | 解析上传的 PDF/Word/PPT/MD |
| `remember` | 工作台 Agent | 写入课程长期记忆（§4.8） |

埋点用结构化日志：

```go
logger.Info("tool_call",
    zap.String("tool", toolID),
    zap.Duration("duration", d),
    zap.String("session_id", sessionID),
    zap.String("status", status),
)
```

理由：

- 工具调用的字段与链路追踪系统中的一根 span 一一对应（`call_id`→span_id、`tool_id`→span name、`request`/`response`→attributes、`duration_ms`→时长、`error_code`→error 状态），其用途是排错，属于可观测性范畴而非业务数据。
- 调用量小——`doc_extract` 一次会话至多几次，`web_search` 更少——业务库不需要为它建表。
- V1 无 worker 租约与重试机制，不需要 `call_id` 提供的幂等去重。
- 若独立成表并挂业务外键 `ON DELETE CASCADE`，课程删除会连带删除排错证据，与可观测性「数据应比被观测对象活得更久」的要求相悖。
- 工具结果很长（`doc_extract` 一份 50 页 PDF 几万字），存进业务库是对素材模块已有副本的重复；就算存下来，拼上下文时也会被滚动摘要压掉（§4.6），换不到任何东西。

---

## 5. 状态值

### 5.1 课程状态 `classrooms.status`

| 状态 | 含义 |
|---|---|
| `draft` | 已创建，尚未开始生成 |
| `outlining` | 正在生成课程大纲 |
| `generating` | 正在逐场景生成内容，首页尚未就绪 |
| `playable` | 首页已就绪，可以进入课堂；后续场景仍在生成 |
| `ready` | 所有场景及其讲解均已生成 |
| `failed` | 生成流程中断，课程不可进入 |

**「首页已就绪」的判据**是 `sort_order = 0` 那个场景的 `content_status` 与 `narration_status` 都为 `ready`，也就是文本和音频都有了。角色不算门槛：`classroom_agents` 是课程级的，随大纲一起产出，它没有「生成中」这个中间态（表上也没有状态字段），卡不住首页。

`playable` 独立成一个值，是为了让课程列表页能区分「还在生成，进不去」和「能进了，后面还在跑」。只有 `generating` 的话，前端得自己去数场景才知道第一页好没好。生成全部完成后由 `playable` 进入 `ready`；整条流程中断则由 `outlining`、`generating` 或 `playable` 进入 `failed`，并写入 `generation_error`。

**服务端启动时必须清理僵尸状态。** 生成任务跑在服务端进程内：用户中途离开不影响它，但服务端自身重启（部署、崩溃）会让任务消失，而 `status` 还停在 `outlining` 或 `generating`——这门课会永远显示「生成中」、永远进不去，且不会再有任何东西来改它。所以启动时要把所有 `status IN ('outlining','generating')` 的课程置为 `failed` 并写入 `generation_error`。V1 不支持断点续传，中断的生成只能回首页重新发起。

`status` 这类枚举列一律用 `varchar + CHECK` 而不是 PostgreSQL 原生 ENUM，后续增加状态时迁移成本更低。

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

## 6. 外键与删除规则

| 子表/字段 | 父表 | 删除动作 |
|---|---|---|
| `classrooms.folder_id` | `folders.id` | `SET NULL` |
| 删除 `classrooms` | 课程从属数据 | `CASCADE` 删除场景、课程角色、课程记忆，以及工作台会话及其下消息 |
| `scenes.classroom_id` | `classrooms.id` | `CASCADE` |
| `classroom_memories.classroom_id` | `classrooms.id` | `CASCADE` |
| `workbench_sessions.classroom_id` | `classrooms.id` | `CASCADE` |
| `workbench_messages.session_id` | `workbench_sessions.id` | `CASCADE` |

课程删除立即级联删除场景、课程角色、课程记忆，以及工作台会话及其下全部数据。工作台会话不允许脱离课程存在，因此 `workbench_sessions.classroom_id` 采用 `CASCADE` 而非 `SET NULL`。

**V1 一律硬删除，没有软删除。** 用户单独删除一个会话时直接删 `workbench_sessions` 那一行，其下消息由 `workbench_messages.session_id` 的 `CASCADE` 一并删除——这条外键就是这个用途；课程被删除时，它同样负责把整门课的会话与消息一起清掉。

素材上传、解析、向量化与 RAG 检索由**其他模块**负责，不在本数据库设计中建表。本设计只提供 `workbench_sessions` 作为素材归属的锚点：素材模块的表通过 `session_id` 外键指向 `workbench_sessions.id`，其自身的删除规则由该模块决定。

课程封面不单独保存图片路径，前端使用该课程 `sort_order = 0` 的首个场景 JSON 渲染封面。

---

## 7. 索引设计

| 表 | 索引 | 目的 |
|---|---|---|
| `folders` | `(created_at DESC)` | 文件夹列表 |
| `classrooms` | `(folder_id, updated_at DESC)` | 文件夹内课程列表 |
| `classrooms` | `(updated_at DESC)` | 最近课程 |
| `scenes` | `UNIQUE(classroom_id, sort_order)` | 场景顺序与读取 |
| `scene_segments` | `UNIQUE(scene_id, sort_order)` | 按播放顺序取某场景的全部讲解段落 |
| `scene_segments` | `UNIQUE(scene_id, content_key)` | 同一场景内一个内容块只对应一个段落 |
| `classroom_agents` | `UNIQUE(classroom_id, sort_order)` | 某课程的全部角色（生成与回放） |
| `classroom_memories` | `(classroom_id)` | 拼上下文时取某门课的长期记忆（`ORDER BY id DESC LIMIT 50`，§4.8） |
| `workbench_sessions` | `(classroom_id, updated_at DESC)` | 某课程下的历史会话列表 |
| `workbench_messages` | `UNIQUE(session_id, sequence)` | 会话顺序读取 |

V1 暂不对大段文本建全文索引；当工作台历史搜索成为真实需求后，再为 `workbench_messages.content` 加 PostgreSQL FTS。素材解析、向量化和 RAG 检索由其他模块单独设计。

---

## 8. 建表迁移顺序

第一批迁移（课程主链路）：

1. 创建通用 `updated_at` 触发器（若采用触发器方案）。
2. 创建 `folders`。
3. 创建 `classrooms`。
4. 创建 `scenes`。
5. 创建 `scene_segments`。

第二批迁移（工作台与 Agent）：

1. 创建 `workbench_sessions`。
2. 创建 `workbench_messages`。
3. 创建 `classroom_memories`。

工作台消息和课程记忆写入 PostgreSQL 成功后，再更新 Redis 缓存；Redis 写入失败不能回滚已经提交的数据库记录。

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

7. **工作台会话必须归属课程。** `classroom_id` 非空、`CASCADE`，不存在任何无课程的会话；进入工作台先选课程，会话在发出第一条消息时才落库（见 §4.6、§6）。
8. **V1 不做「撤回课件修改」。** 工作台的修改直接写入 `scenes`，不保留修改前的内容，因此没有 `scene_backups` 之类的备份表。将来要做撤回时再加：备份行可按「一次用户消息 = 一个撤销批次」组织，用触发修改的那条 `workbench_messages.id` 作为批次标识，一条 SQL 整批还原。
9. **V1 工具调用只写结构化日志，不建 `tool_calls` 表**（见 §4.9）。

**课程与角色**

10. **课程实际角色统一写入 `classroom_agents`。** 包括教师、手动预设角色和自动模式最终选中的角色，并保存提示词、音色、头像、颜色快照（见 §4.3）。

**实施节奏**

11. **保留 JWT 与鉴权中间件骨架，但 V1 不启用。** `pkg/jwt/`、`internal/middleware/auth.go` 与 `configs` 中的 `jwt` 配置段保留，作为后续迭代的骨架；当前零调用、不挂路由（见 §1.1）。
12. **工作台相关的三张表本轮只设计、不立即建表。** `workbench_sessions`、`workbench_messages`、`classroom_memories` 本轮只出设计，迁移脚本在后续迭代生成。

---

本文档的 V1 数据库设计到此定稿。后续按 §8 的迁移顺序编写 SQL migration 与 GORM Model。
