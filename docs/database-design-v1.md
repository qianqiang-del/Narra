# Narra V1 数据库设计（定稿）

> 状态：已定稿（2026-09-14）  
> 适用版本：本地单机部署 V1  
> 数据库：PostgreSQL 16+  
> 最后更新：2026-09-14

---

## 1. 设计目标与边界

Narra V1 是本地部署的个人/团队内使用版本。数据库只保存业务数据与运行记录；模型、搜索、语音等第三方服务的密钥均由配置文件或环境变量管理，不写入数据库。

### 1.1 V1 明确不做

- **不启用**登录、注册、鉴权、用户、角色、权限和多租户：不在任何路由上挂鉴权中间件，不校验任何身份。
- 不创建 `users`、`roles`、`permissions`、`api_keys` 等表。
- 不在数据库保存模型 API Key、数据库密码、TLS 私钥或其他密钥。
- **V1 不做 Pro 工作台**：工作台会话、消息、课程记忆这三张表及其相关接口整体推迟到 V2（见 §9.5）。
- 不用 Redis 当唯一存储：Redis 只用于加速读取与实时任务状态。

关于鉴权代码：仓库中已存在 JWT 与鉴权中间件的骨架（`pkg/jwt/`、`internal/middleware/auth.go`）以及 `configs` 中的 `jwt` 配置段。这是**为后续迭代预留的骨架，V1 不启用**——当前无任何调用方，不挂载到路由，配置加载也不校验 `jwt.secret`。将来启用时需同时完成两件事：替换 `jwt.secret` 的占位值，并按 §9.1 创建用户表。

### 1.2 V1 要支持

- 创建、整理、删除和恢复本地课程。
- 一节课程有多个有序场景（讲解、测验、互动、项目实践、完成页）。
- 课程生成在后台进行：用户可中途离开，回来按课程状态继续查看；首页场景的内容与讲解都生成完毕即可进入课堂，其余场景继续生成。生成中断时保留原因，用户回首页重新发起；若中断时首页已经就绪，课程仍可进入，只是剩余场景不会再生成（见 §5.1）。
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
                 └───── 0..N classroom_agents
```

### 2.1 关系说明

- 文件夹可为空：未归档课程的 `classrooms.folder_id` 为 `NULL`。
- 课程是核心聚合根；场景、角色快照与讲解段落均从属于课程。

---

## 3. 通用约定

| 项目 | 约定 |
|---|---|
| 主键 | `bigint` + `GENERATED ALWAYS AS IDENTITY`（即 `bigserial` 语义）；Go 侧对应 `uint64` |
| 时间 | `timestamptz`，统一存 UTC |
| 删除策略 | **全部硬删除，无软删除**；文件夹和课程直接删除，课程的从属数据（场景、讲解段落、课程角色）级联删除 |
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

**文本列的长度是硬约束，不是提示。** PostgreSQL 的 `varchar(n)` 超长是**报错**，不是静默截断——一次超长会回滚整个事务，把同一批已经写好的数据一起丢掉。因此凡是可能由大模型产出或来自用户自由输入的 `varchar` 列，写入前必须在应用层**按字符**（不是按字节）截断到上限以内；数据库的长度约束只负责拦下漏网的脏数据。目前属于这一类的是 `classroom_agents.name` / `persona`、`scenes.title`、`scene_segments.content_key`。

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
| `generation_error` | `text` | 可空 | 这次生成没能把课做完整的原因；为空表示课程生成完整、不必再等。`failed` 与 `playable` 上都可能出现，见 §5.1 |
| `generation_config` | `jsonb` | 非空，默认 `{}` | 模型、搜索、解析器等本次生成快照 |
| `agent_config` | `jsonb` | 非空，默认 `{}` | 角色选择模式、自动生成策略与 TTS 配置 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 更新时间 |

约束：

- `CHECK (mode IN ('vocational', 'interactive'))`。
- `CHECK (status IN ('generating', 'playable', 'ready', 'failed'))`，与 §5.1 一一对应；增减状态值时必须同时改这里。

生成状态直接记在本表上，**没有独立的生成任务表**。V1 一次生成只跑一趟：不并发、不重试、不取消、不支持断点续传，因此「这门课生成到哪了」就是 `status`、「为什么没做完」就是 `generation_error`。将来若开放「对同一门课重新生成」，才需要单独的表记录每一趟任务。

**`generation_error` 有值 ⇔ 这门课不会再生成了。** 它和 `status` 回答的是两个不同的问题：`status` 只回答「**能不能进课堂**」，这一列只回答「**还会不会继续生成**」。两者是正交的，所以 `playable` 配一个非空的 `generation_error` 是合法状态，不是脏数据。

会写这一列的有两种情况：

- **流程中断**——进程挂了、崩溃了，剩下的场景永远不会再生成。
- **流程跑完了，但带着洞**——某些页面生成失败被跳过（§4.4），而 V1 没有单页重试（§5.1），这些洞就永远留在那儿了。这种情况尤其容易漏：流程自己跑到了终点，`status` 停在 `playable`，没有任何东西会再来改它；不留一条记录的话，前端会一直显示「正在生成」，等一个不会来的结果。

相应地，`status = 'ready'` 的含义收紧为「**全部完整生成，没有洞**」。

**这一列有值不等于课程进不去。** 流程中断时如果首页已经生成好（`status = 'playable'`），课程仍然可以进入，只是后面的场景不会再补上。把这种课打成 `failed` 是错的：它在中断前用户明明进得去，打完反而进不去了。

`generation_error` 与 `scenes.error_message` 分工不同：后者记的是「这一页为什么没生成出来」，记的时候整条流程还在往下跑；前者记的是「这门课为什么没做完」。大纲阶段挂掉时一条场景都还没建，`scenes` 表里空无一物，这一列是唯一的失败记录。两者同样只写能给人看的摘要，禁止写入原始 API 响应、堆栈和密钥，落库前按字符截断（`generation_error` 同样建议 500）。

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

这张表保存一节课程最终实际使用的角色。它不是全局角色库；角色的默认定义、系统提示词模板和可用音色目录仍由代码/配置维护。课程生成时将最终使用的角色信息复制到本表，保证后续回放不受默认配置变化影响。

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
| `status` | `varchar(32)` | 非空，CHECK | 场景状态，见 5.2；`ready` 表示页面 JSON 与全部讲解段落都已就绪 |
| `content` | `jsonb` | 非空，默认 `{}` | 场景内容，统一为 `{"blocks":[...]}`，前端按每个 block 的 `type` 渲染 |
| `error_message` | `text` | 可空 | 本场景生成失败的错误摘要，写入规则见下 |
| `created_at` | `timestamptz` | 非空 | 创建时间 |
| `updated_at` | `timestamptz` | 非空 | 更新时间 |

约束：

- `UNIQUE (classroom_id, sort_order)`，普通唯一约束即可。
- `CHECK (type IN ('slide', 'quiz', 'interactive', 'pbl', 'complete'))`。
- `CHECK (status IN ('pending', 'generating', 'ready', 'failed'))`，与 §5.2 一一对应。

**`sort_order` 从 0 开始、连续、不重复。** 生成流程按顺序逐页插入，插进去之后没有任何入口会调换顺序，所以普通唯一约束就够——它保证同一门课里不会有两页占同一个位置。

V1 没有「重排页面」这个功能（Pro 工作台整体推迟到 V2，见 §9.5）。**将来做工作台时，这条约束要改成 `DEFERRABLE INITIALLY IMMEDIATE`**：重排要把受影响的 `sort_order` 整体挪位，挪的过程中必然出现两行暂时同号——把原来第 3 页改成 4 时，第 4 页还是 4——而普通唯一约束在**每条语句结束时**就检查，会当场报冲突，重排根本做不下去。改成可延迟之后，重排那个事务在开头加一句 `SET CONSTRAINTS scenes_classroom_id_sort_order_key DEFERRED;` 把检查推到 `COMMIT`，中间态的同号在提交前自己消失（注意这条语句的作用域只到事务结束）。代价是**可延迟的唯一约束不能当 `INSERT ... ON CONFLICT` 的仲裁者**，PostgreSQL 明确不支持，将来要在这张表上做 upsert 时一并考虑。

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
- `UNIQUE (scene_id, sort_order)`，保证播放顺序稳定；普通唯一约束即可，理由与 `scenes` 那条相同（V1 不重排，见 §4.4）。将来工作台落地时两条要一起改成可延迟，讲解顺序会跟着页面重排一起挪位。
- `content_key` 必须能在所属场景 JSON 的 `blocks` 中找到；内容块的 `type` 只从 JSON 读取。
- `CHECK (status IN ('pending', 'generating', 'ready', 'failed'))`。
- `CHECK (status <> 'ready' OR audio_path IS NOT NULL)`：`ready` 的含义就是「这一段能播了」，所以必须已经有音频；`pending`、`generating`、`failed` 下允许为空。音频时长不落库，前端从音频元素自身读（`HTMLAudioElement.duration`）。

**`status` 覆盖讲稿与 TTS 两关，但只有一个值**，所以失败时要靠 `text` 区分是哪一关挂的：

- `text` 非空 + `failed` → 讲稿已经写出来了，挂的是 TTS 合成。
- `text` 为空 + `failed` → 讲稿本身没生成出来。

V1 **不提供重试**（见 §5.1），所以这个区分只用来告诉用户「这一段卡在哪一关」，不触发任何自动动作。

`text` 是 `not null`，讲稿还没生成时存空串而不是 `NULL`——这是上面这条判断能成立的前提。

前端渲染约定：

```html
<p data-content-key="paragraph-1">变量可以理解为一个带名字的盒子。</p>
```

播放第 N 段时，前端根据 `content_key` 查找对应 DOM 元素，执行高亮、滚动和播放同步。不能只依赖数组下标，因为用户编辑或重新排序场景内容后，下标可能发生变化。

**本表不记录发言角色。** 讲解只由教师发声（§4.3），存下来就是个恒等于教师行的常量，还得额外校验它属不属于本课程——而这条「必须属于同一课程」的约束从本表这一侧根本写不成普通外键（本表只直接挂着 `scene_id`，要经由 `scenes.classroom_id` 才推得出课程）。所以干脆不存：发言角色就是教师，由 `classroom_agents` 里 `agent_key = 'teacher'` 那一行确定，`UNIQUE (classroom_id, agent_key)` 已经保证了它唯一。

教师音色同样不在本表重复保存，从 `classroom_agents.voice_id` 读取。将来若开放多角色讲解，再加一列发言角色外键。

### 4.6 工具调用：V1 不建表

课堂 Agent 的工具调用**不进入业务数据库**：调用与结果由**链路追踪模块**统一采集（该模块的可观测设计不在本文件范围），本设计只在工具调用处埋点。业务库这一侧什么都不存。

V1 只有一个 Agent 会调工具，就是课堂 Agent（§4.3）：

| 工具 | 谁用 | 说明 |
|---|---|---|
| `web_search` | 课堂 Agent | 网络搜索，用来补充课程内容 |
| `doc_extract` | 课堂 Agent | 解析上传的 PDF/Word/PPT/MD |
| `tts` | 课堂 Agent | 把讲解文本合成成音频，写 `scene_segments.audio_path` |

埋点用结构化日志：

```go
logger.Info("tool_call",
    zap.String("tool", toolID),
    zap.String("classroom_id", classroomID),
    zap.Duration("duration", d),
    zap.String("status", status),
)
```

理由：

- 工具调用的字段与链路追踪系统中的一根 span 一一对应（`call_id`→span_id、`tool_id`→span name、`request`/`response`→attributes、`duration_ms`→时长、`error_code`→error 状态），其用途是排错，属于可观测性范畴而非业务数据。
- 调用量小——`doc_extract` 一次会话至多几次，`web_search` 更少——业务库不需要为它建表。
- V1 无 worker 租约与重试机制，不需要 `call_id` 提供的幂等去重。
- 若独立成表并挂业务外键 `ON DELETE CASCADE`，课程删除会连带删除排错证据，与可观测性「数据应比被观测对象活得更久」的要求相悖。
- 工具结果很长（`doc_extract` 一份 50 页 PDF 几万字），存进业务库是对素材模块已有副本的重复，换不到任何东西。

---

## 5. 状态值

### 5.1 课程状态 `classrooms.status`

| 状态 | 含义 |
|---|---|
| `generating` | 正在生成（大纲与场景内容算在一起），首页尚未就绪，进不去 |
| `playable` | 首页已就绪，可以进入课堂；后续场景仍在生成，或流程已中断不再生成（看 `generation_error`） |
| `ready` | 所有场景及其讲解均已生成，没有任何一页失败 |
| `failed` | 生成中断，且首页尚未就绪，课程不可进入 |

只有四个值，不要往回加。曾经的 `draft`（已创建、未开始）和 `outlining`（正在生成大纲）都已删除：课程建出来就开始跑，没有「等用户确认大纲」这一步；大纲和逐页内容在前端都显示成「正在生成」，用户分不出区别，后端自己知道走到哪一步就够了，不需要往库里写。

**「首页已就绪」的判据**是 `sort_order = 0` 那个场景的 `status = 'ready'`，也就是页面 JSON 和它的讲解音频都有了。角色不算门槛：`classroom_agents` 是课程级的，随大纲一起产出，它没有「生成中」这个中间态（表上也没有状态字段），卡不住首页。

`playable` 独立成一个值，是为了让课程列表页能区分「还在生成，进不去」和「能进了，后面还在跑」。只有 `generating` 的话，前端得自己去数场景才知道第一页好没好。生成全部完成后由 `playable` 进入 `ready`。

**生成结束时 `status` 怎么变，要看首页好没好：**

| 情况 | `status` | `generation_error` | 用户看到 |
|---|---|---|---|
| 流程中断，首页还没好 | `failed` | 有值 | 进不去，只能回首页重新发起 |
| 流程中断，首页已经好了 | 留在 `playable` | 有值 | 照样能进课堂，后面的场景不会再补上 |
| 流程跑完了，但有页面失败 | 留在 `playable` | 有值 | 同上 |
| 流程跑完了，没有洞 | `ready` | 空 | 全部页面都能看 |

`status` 只回答「**能不能进**」，`generation_error` 只回答「**还会不会继续生成**」，两者是正交的。所以 `playable` 配一个非空的 `generation_error` 是合法状态，不是数据错误：能进，但后面几页没了。把中断的 `playable` 一并打成 `failed` 是错的——那门课在中断前用户明明进得去，打完反而进不去了。

**服务端启动时必须清理僵尸状态。** 生成任务跑在服务端进程内：用户中途离开不影响它，但服务端自身重启（部署、崩溃）会让任务消失，而 `status` 还停在 `generating` 或 `playable`——这门课的状态不会再有任何东西来改它。所以启动时这两类都要扫一遍，按上表前两行分流：

- `status = 'generating'` → 置为 `failed`，并写入 `generation_error`。
- `status = 'playable'` → **保持 `playable` 不动**，只写入 `generation_error`。

`ready` 不扫：它表示流程已经跑到终点，重启不影响它，扫了反而会把好好的课误判成中断的。

上表第三行（跑完了但带着洞）不归启动清理管，而是**生成流程自己收尾时**的责任：流程正常跑完后，先检查是否所有场景的 `status` 都是 `ready`。是则置 `ready`；只要有一个不是，就置 `playable` 并写入 `generation_error`（写明是哪几页没出来），不能让它无声无息地停在 `playable`。

场景级的僵尸状态（`scenes.status`、`scene_segments.status` 卡在 `generating`）同样在启动清理时一并扫掉，规则与课程一致：所属课程的 `status` 被置 `failed` 的，它那些 `generating` 的场景与段落一并置 `failed`；课程留在 `playable` 的，只把它卡住的那些行置 `failed`，不能连累已经生成好的页。

三种情况都要写 `generation_error`，这是用户唯一能看到「为什么没做完」的地方。V1 不支持断点续传，也不支持重试，中断或带洞的生成只能回首页重新发起；停在 `playable` 的课还能继续看已经生成好的部分。

`status` 这类枚举列一律用 `varchar + CHECK` 而不是 PostgreSQL 原生 ENUM，后续增加状态时迁移成本更低。

### 5.2 场景状态 `scenes.status`

| 状态 | 含义 |
|---|---|
| `pending` | 已有大纲，等待生成 |
| `generating` | 正在生成页面 JSON 或讲解段落 |
| `ready` | 页面 JSON 与全部讲解段落均已生成，可完整播放 |
| `failed` | 生成失败；跳过该场景、继续生成后面的，这一页不会再补齐 |

只有一个状态，不拆「页面内容」和「老师讲解」两个。曾经拆过，理由是「页面已经生成，但讲解还在生成」是个用户能感知的中间态；实际上进门条件本来就是**文本和音频都齐**，用户不会在文本齐了音频没齐时被放进去，这个中间态没有任何消费方——前端 `data/scenes.ts` 也一直只有一个 `SceneStatus` 字段。所以 `ready` 的含义定为「页面 JSON 与它全部讲解段落都已就绪」，两关合一关。

讲解段落各自的进度在 `scene_segments.status`（§4.5）。`scenes.status` 是这些段落的**汇总**：页面 JSON 生成完只算走了一半，必须等本场景下每个段落都 `ready`，本场景才置 `ready`。只要还有一个段落不是 `ready`，场景就不是 `ready`。两个状态不会打架，因为汇总只有一个方向：段落先动，场景跟着动。

**任何一页 `failed` 都会让整门课永远到不了 `ready`。** V1 没有单页重试（§5.1），这些洞不会被补上。生成流程跑到终点时，课程停在 `playable` 并把原因写进 `classrooms.generation_error`（§4.2），前端据此显示「后面几页没能生成出来」，而不是一直转「正在生成」。

`complete` 不再作为场景状态；课程完成页仍通过 `scenes.type = complete` 表示。

## 6. 外键与删除规则

| 子表/字段 | 父表 | 删除动作 |
|---|---|---|
| `classrooms.folder_id` | `folders.id` | `SET NULL` |
| `scenes.classroom_id` | `classrooms.id` | `CASCADE` |
| `scene_segments.scene_id` | `scenes.id` | `CASCADE` |
| `classroom_agents.classroom_id` | `classrooms.id` | `CASCADE` |
| 删除 `classrooms` | 以上全部从属数据 | `CASCADE`，沿上述外键逐级传递 |

**注意 `scene_segments` 是二级级联，不是直接挂在课程上的。** 它只有 `scene_id` 一个外键，删除课程时靠 `scenes.classroom_id` 先把场景删掉，再由 `scene_segments.scene_id` 把讲解段落带走——`CASCADE` 会沿着外键链自己传下去，应用层不用手写两次删除，但两个外键都必须是 `CASCADE`，中间断一环就会剩下孤儿行（或者直接删不掉课程）。

课程删除立即级联删除场景及其讲解段落、课程角色。**V1 一律硬删除，没有软删除**，因此没有任何表的删除需要应用层先清理下级。

素材上传、解析、向量化与 RAG 检索由**其他模块**负责，不在本数据库设计中建表。**素材归属的锚点是 `classrooms.id`**：素材模块的表通过 `classroom_id` 外键指向 `classrooms.id`，其自身的删除规则由该模块决定。V1 没有工作台会话，素材不可能挂在会话上（§9.5）。

课程封面不单独保存图片路径，前端使用该课程 `sort_order = 0` 的首个场景 JSON 渲染封面。

---

## 7. 索引设计

| 表 | 索引 | 目的 |
|---|---|---|
| `folders` | `(created_at DESC)` | 文件夹列表 |
| `classrooms` | `(folder_id, updated_at DESC)` | 文件夹内课程列表 |
| `classrooms` | `(updated_at DESC)` | 最近课程 |
| `scenes` | `UNIQUE(classroom_id, sort_order)` | 场景顺序与读取（§4.4） |
| `scene_segments` | `UNIQUE(scene_id, sort_order)` | 按播放顺序取某场景的全部讲解段落（§4.5） |
| `scene_segments` | `UNIQUE(scene_id, content_key)` | 同一场景内一个内容块只对应一个段落 |
| `classroom_agents` | `UNIQUE(classroom_id, sort_order)` | 某课程的全部角色（生成与回放） |
| `classroom_agents` | `UNIQUE(classroom_id, agent_key)` | 同一预设角色在课内唯一，同时保证教师只有一行（§4.3） |

`classroom_agents` 的两条唯一约束都以 `classroom_id` 打头，查某门课的全部角色走得到索引，不再单独建；`scenes`、`scene_segments` 同理，各自的外键列都被唯一约束覆盖，级联删除也不会全表扫。

V1 暂不对大段文本建全文索引，也没有需要全文检索的表（工作台推迟到 V2，§9.5）。素材解析、向量化和 RAG 检索由其他模块单独设计。

---

## 8. 建表迁移顺序

**V1 只有一批迁移，五张表，按下面的顺序建**（顺序由外键依赖决定，不能打乱）：

1. 创建通用 `updated_at` 触发器函数 `set_updated_at()`。
2. 创建 `folders`。
3. 创建 `classrooms`（依赖 `folders`）。
4. 创建 `classroom_agents`（依赖 `classrooms`）。
5. 创建 `scenes`（依赖 `classrooms`）。
6. 创建 `scene_segments`（依赖 `scenes`）。

**必须用 SQL 迁移脚本，不能用 `AutoMigrate`。** 这套设计里有三样东西 GORM 的 tag 表达不了，`AutoMigrate` 一个都建不出来：

- **`CHECK` 约束**：状态值合法性、`sort_order >= 0`、`status <> 'ready' OR audio_path IS NOT NULL`。没有它们，写错的枚举值会安静落库，前端查不到、页面一直转圈。
- **`updated_at` 触发器**：函数和 trigger 都不在 `AutoMigrate` 的职责里。而启动清理僵尸状态走的是直接 UPDATE，GORM 钩子拦不住，`updated_at` 不会更新。
- **外键**：本项目配了 `DisableForeignKeyConstraintWhenMigrating: true`，`AutoMigrate` 根本不建外键，§6 那张级联删除规则表会全部落空。

另外 `AutoMigrate` 只加不减（不删字段对应的列），本来也不适合当迁移工具。实体（`internal/model/entity/`）只负责读写，建表以迁移脚本为准。

工作台相关的三张表不在本批迁移里（§9.5）。

---

## 9. 未来扩展（不进入 V1 迁移）

### 9.1 增加用户体系

后续创建 `users` 后，可在 `folders`、`classrooms` 添加可空 `owner_id`，完成历史数据迁移后再改为非空。V1 不应预先放一个无意义的 `user_id` 或固定“本地用户”。

代码侧的对应动作：启用 §1.1 中保留的 JWT 与鉴权中间件骨架，替换 `jwt.secret` 占位值，并把 `Auth()` 挂到需要保护的路由组上。因此本次迭代只需新增表和迁移，不必从零搭建鉴权基础设施。

### 9.2 多层文件夹

若需要目录树，再向 `folders` 加 `parent_id bigint NULL REFERENCES folders(id)`；当前单层文件夹足以匹配现有前端。

### 9.3 Agent 版本管理

当人设可被界面编辑或需要版本回放时，再新增 `agent_profiles` 与 `agent_profile_versions`；目前使用 `agent_config` 快照即可。

### 9.4 向量与 RAG

RAG 文档切片、embedding 模型版本及向量索引由知识管线模块单独设计。不要把它们混入本 V1 业务主库的第一批迁移。

### 9.5 Pro 工作台（整体推迟到 V2）

V1 不做 Pro 工作台，因此三张表一并推迟，**V1 不建、也不写对应迁移**：`workbench_sessions`（会话）、`workbench_messages`（消息）、`classroom_memories`（课程长期记忆）。

设计没有丢，在模块文档 `docs/modules/agent-mcp-tools.md` 的「上下文记忆系统」一节；本文件里那三张表的字段设计已经删掉，真做工作台时按那份设计回来补。

推迟带来的两个连带改动，已经落到正文：

- **素材归属的锚点从会话改为课程**（§6）：素材本来挂在 `workbench_sessions` 上，没有会话了就挂 `classrooms.id`。
- **「撤回课件修改」不再是 V1 要交代的事**：课件在生成之后没有任何入口可以改，不存在误改，也就不需要备份表（§10 第 5 项）。

---

## 10. 审核结论

以下各项已全部确认，作为编写 SQL migration 与 GORM Model 的前提。

**数据库与范围**

1. **PostgreSQL 为 V1 唯一关系型数据库。** Redis 只作缓存与实时状态，不作唯一存储（见 §1.1）；不引入第二种关系型数据库。
2. **V1 无任何用户表、登录表和鉴权持久化表。** 不创建 `users`、`roles`、`permissions`、`api_keys` 等表；业务记录不依赖用户表。未来扩展路径见 §9.1。
3. **素材上传、解析与 RAG 检索由其他模块负责。** 本设计只提供 `classrooms.id` 作为素材归属锚点，不在本库建素材表，其删除规则由该模块决定（见 §6）。
4. **主键统一使用 `bigint` + `GENERATED ALWAYS AS IDENTITY`（Go 侧 `uint64`），不使用 UUID。** 因此也不需要 `pgcrypto`（见 §3、§8）。
5. **V1 不做 Pro 工作台，也没有「撤回课件修改」。** 会话、消息、课程记忆三张表整体推迟到 V2（见 §9.5）。相应地，课件在生成之后没有任何入口可以修改，不存在误改，因此也没有 `scene_backups` 之类的备份表。

**删除规则**

6. **文件夹硬删除，下辖课程保留。** 删除文件夹只删除文件夹行本身；该文件夹下所有课程的 `classrooms.folder_id` 置为 `NULL`，课程变为未归档状态，**不会被删除**（见 §4.1、§6）。
7. **课程硬删除，从属数据级联删除。** 删除课程时，其场景及其讲解段落、课程角色一并级联删除（见 §6）。

**课程与角色**

8. **课程实际角色统一写入 `classroom_agents`。** 包括教师、手动预设角色和自动模式最终选中的角色，并保存提示词、音色、头像、颜色快照（见 §4.3）。
9. **V1 工具调用只写结构化日志，不建 `tool_calls` 表**（见 §4.6）。

**实施节奏**

10. **保留 JWT 与鉴权中间件骨架，但 V1 不启用。** `pkg/jwt/`、`internal/middleware/auth.go` 与 `configs` 中的 `jwt` 配置段保留，作为后续迭代的骨架；当前零调用、不挂路由（见 §1.1）。

---

本文档的 V1 数据库设计到此定稿。后续按 §8 的迁移顺序编写 SQL migration 与 GORM Model。
