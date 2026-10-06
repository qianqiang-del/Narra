# 数据库迁移

V1 六张表：`folders`、`classrooms`、`preset_agents`、`classroom_agents`、`scenes`、
`scene_segments`。

其中 `preset_agents` 是**角色池**（人工维护的角色清单），`classroom_agents` 是
**课堂与角色的关联表**——它只存 `classroom_id` / `agent_id` / `voice_id`，
角色的名称、人设、头像都去 `preset_agents` 查。

知识库另有五张表（`embedding_models`、`knowledge_documents`、
`knowledge_upload_records`、`knowledge_chunks`、`knowledge_embeddings`）和一张配置表
`embedding_settings`。

其中 `knowledge_documents` 是**资产**（正文、切片、向量的父表），
`knowledge_upload_records` 是**投递历史**（谁在什么时候传了什么文件、成没成）。
两者靠 `document_id` 弱关联（外键 ON DELETE SET NULL）：删掉一条投递记录不动那份知识，
删掉一份知识也不会让投递历史消失 —— 那条记录的 `document_id` 变成空，
界面上读作"已收录后删除"。

## 执行顺序

> **Windows 上跑这些脚本前先设 `PGCLIENTENCODING=UTF8`。** 脚本注释都是中文，而 psql
> 默认按客户端 locale（本机是 GBK）读文件，中文会撞上
> `编码"GBK"的字符 0x.. 在编码"UTF8"没有相对应值`，整条命令中止、什么也没执行。
> 命令写成 `PGCLIENTENCODING=UTF8 psql ...`，或先 `chcp 65001`。

**第一步，建表 + 全部约束** —— 启动服务时自动完成（`internal/app/app.go` 的
`initDatabase`）：先 `CREATE EXTENSION IF NOT EXISTS vector`，再 `AutoMigrate`。
表、列、类型、CHECK、外键、唯一、索引全部在这一步建好，声明见实体字段上的 tag
（`internal/model/entity/*.go`）。

例外有两个，都是**检索用的表达式索引**，没法声明在实体 tag 上：

- **HNSW 向量索引**按模型分片（`WHERE model_id = ?`，维度也随模型变），在确定默认模型后
  自动补建（`internal/repository/vector_index.go` 的 `EnsureVectorIndex`，幂等、维度变了会重建）；
- **词法 GIN 索引**建在「切片正文 + 章节标题」的表达式上，按数据库能装的扩展选
  pg_bigm / pg_trgm（`internal/repository/lexical_index.go` 的 `EnsureLexicalIndex`，
  幂等、换扩展会重建）。

两者都详见 `docs/rag-database.md`「向量索引」「词法索引」。也就是说：
**没有任何一条需要手工执行的索引 SQL**。

```bash
go run ./cmd/server -c configs/config.yaml
```

**第二步，灌角色池初始数据**（只需要跑一次）：

```bash
psql "postgresql://postgres:密码@localhost:5432/narra" -f migrations/0002_seed_preset_agents.sql
```

灌的是**初始值**，不是权威值：角色池以后由人工直接维护数据库，
加角色、改角色不用回改这个文件。

**第三步，回填上传记录**（只需要跑一次）：

```bash
psql "postgresql://postgres:密码@localhost:5432/narra" -f migrations/0003_seed_upload_records.sql
```

上传记录表上线时，库里已有的 `import` 文档还没有对应的投递记录（那张表当时不存在），
这份脚本按现有文档补一次历史，让"上传记录"抽屉不至于从零开始。

灌的同样是**推断值**：文件字节数在记录表之前没有被记下来过，只能填 0；投递时间借文档的
创建时间。只处理 `source_type = 'import'` 的文档 —— `manual`（编辑框录入）没有"上传"这回事。
脚本幂等（`NOT EXISTS` 兜底），全新环境跑也是 0 行，可以放心重复执行。

**第四步，把历史的失败原因改成中文**（只需要跑一次）：

```bash
psql "postgresql://postgres:密码@localhost:5432/narra" -f migrations/0004_localize_failure_reasons.sql
```

在此之前，失败原因落库的是 Go 侧 error 字符串化的诊断串
（`PARSER_FAILED: 文档解析失败 (stderr: parser failed: No module named 'scipy')`），
界面直接显示它就成了中英混杂的机器串。这份脚本摘掉错误码前缀与 stderr 尾巴，
原串存进 `metadata.error_detail`、错误码存进 `metadata.error_code` ——
与新写入的行结构一致，排障信息一条不少。幂等，全新环境跑也是 0 行。

**第五步，补回丢失的 upload_path**（只需要跑一次，且只对踩过坑的环境有意义）：

```bash
psql "postgresql://postgres:密码@localhost:5432/narra" -f migrations/0005_restore_upload_paths.sql
```

2026-09-21 之前，收录失败时的 `MarkFailed` 是整块覆盖 metadata，把 `upload_path`
一起抹掉了 —— 而它是重试的输入。于是失败原件明明还留在 `data/uploads/failed/<文档ID>/`
下，点重试也会被判「原件不在」。这份脚本给这类行补回指针，清单是**按本机实况写死**的
（脚本探不了文件系统），换目录就要跟着改；幂等，重复执行 0 行。

**第六步（仅老环境，可选），清掉 updated_at 触发器与函数**：

跑过 2026-09-17 之前版本的环境里留有 18 个 `trg_*_updated_at` 触发器和一个
`set_updated_at()` 函数。删除它们的脚本原先叫 `0005_drop_updated_at_triggers.sql`，
**该文件已移除**——建触发器的迁移文件都没了，新建的库不会再产生它们，固定列表已无意义。
需要时直接跑下面这段，幂等、可反复执行：

```sql
-- 删掉全部 trg_*_updated_at 触发器（按 catalog 现查，不写死表名）
DO $$
DECLARE r record;
BEGIN
  FOR r IN SELECT c.relname AS tbl, t.tgname AS trg
             FROM pg_trigger t
             JOIN pg_class c ON c.oid = t.tgrelid
             JOIN pg_namespace n ON n.oid = c.relnamespace
            WHERE n.nspname = 'public'
              AND NOT t.tgisinternal
              AND t.tgname LIKE 'trg\_%\_updated_at'
  LOOP
    EXECUTE format('DROP TRIGGER IF EXISTS %I ON %I', r.trg, r.tbl);
  END LOOP;
END $$;

-- 函数必须在触发器全删之后：触发器依赖它
DROP FUNCTION IF EXISTS set_updated_at();
```

全新环境不用跑。注意 `DROP TABLE` **不会**连带删掉 `set_updated_at()`（函数是独立对象，
只有触发器随表走），所以「删表重建」的老库可能残留一个孤儿函数，上面第二句能顺手清掉；
`DROP DATABASE` 重建则不会残留。

**第七步（2026-09-25 之前建的老环境，一次性），清理三处已删对象**：

有两类对象「代码里已删、AutoMigrate 不会替存量库处理」，要手动清一次：`source_type`
的取值集合删掉了 `api`（CHECK 只建不换），`knowledge_chunks` 的 `token_count` /
`metadata` 两个无用列也删了（列只加不减，依据见文末「改结构时」）。一个脚本一次做完：

```bash
psql "postgresql://postgres:密码@localhost:5432/narra" -f migrations/0006_cleanup_removed_fields.sql
```

脚本整段在一个事务里：先核对没有 `source_type = 'api'` 的历史行（有则报错并给出
排查 SQL，不会先把约束删掉再失败），再重建 CHECK、删两列。幂等，可重复跑；
新环境不用跑——tag 直接建出收紧后的约束，也没有那两个列。

**第八步（2026-09-30 之前建的老环境，一次性），清理 `embedding_models` 的冗余列**：

```bash
psql "postgresql://postgres:密码@localhost:5432/narra" -f migrations/0007_drop_embedding_model_dead_columns.sql
```

`provider` / `base_url` / `model_version` / `enabled` 四列因无人读写已从实体删除
（协议与地址属于"怎么连服务"，只归 `embedding_settings`，见 `docs/rag-database.md`）。
AutoMigrate 只加不减，存量库要手动删；脚本幂等，新环境不用跑。

⚠️ **顺序：停掉旧版服务 → 跑脚本 → 再启动新版。** 两边各有一条硬约束：
旧版代码仍会向这四列写入（删列后会报 `column does not exist`）；新版代码不再写
`provider` / `enabled`，而它们在旧表上是 `NOT NULL` 且没有默认值（不删列会违反非空
约束，设置页保存与启动对齐都会失败）。迁移与新版代码必须一起上。

**第九步（2026-10-01 之前收录过文档的老环境，一次性），重建旧切法的切片**：

```bash
psql "postgresql://postgres:密码@localhost:5432/narra" -f migrations/0008_rechunk_legacy_chunks.sql
```

切分在 2026-10-01 改为"结构优先"（goldmark 认节树、按子树预算递归，见
`docs/rag-database.md`「切分：结构优先」）。升级只对新收录生效——「重新向量化」
只重算向量、不重切（worker 看到切片还在就直接进 embed 阶段）。本脚本把"还带旧切法
切片"的 ready 文档清掉切片、改回 `pending` + `chunk` 阶段，由 worker 用
`documents.content` 重新切分并向量化。判据 `section_path IS NULL` 幂等，新环境跑命中
0 篇；失败的行照常是"失败可重试"形态。

⚠️ **顺序：先用新代码重启服务 → 再跑脚本。** 没重启时 `section_path` 列还不存在，
脚本会直接报错中止（不会误删）；服务若还是旧代码，旧 worker 会用旧切法重建，等于
白跑。重嵌入要花时间与上游额度、处理期间文档暂不可检索，建议低峰执行。

**第十步（2026-10-01 启用文档类型判定之前收录过文档的老环境，一次性），重建旧切片并补类型元数据**：

```bash
psql "postgresql://postgres:密码@localhost:5432/narra" -f migrations/0009_rechunk_pre_type_detection.sql
```

切分入口现在会先做**文档类型判定**（Go/JSON 用解析器验证，代码按结构切，文档不变，
见 `docs/rag-database.md`「切分入口先判类型」），代码切片还带 `content_type` / `language` /
`symbol` / `symbol_type` 四列。升级只对新收录生效，本脚本把"旧切法切出的 ready 文档"
清掉切片、改回 `pending + chunk`，由 worker 用 `documents.content` 重新切分并向量化。
判据 `content_type IS NULL` 幂等（新切法产出的每一片都带 `content_type`），
新环境跑命中 0 篇；失败的行照常是"失败可重试"形态。

⚠️ **0008 已被本脚本取代，不要再重跑。** 0008 的判据 `section_path IS NULL` 对
"代码 / 无标题文本"永远成立（新切法同样不写 section_path），重复执行会反复把这些
文档重新入队、白烧向量化额度。要从更老的版本升级，直接跑 0009 即可。

⚠️ **顺序：先用新代码重启服务 → 再跑脚本。** 没重启时 `content_type` 列还不存在，
脚本会直接报错中止（不会误删）；服务若还是旧代码，旧 worker 不会写 `content_type`，
脚本会反复命中。重嵌入要花时间与上游额度、处理期间文档暂不可检索，建议低峰执行。

**第十一步（2026-10-02 之前上传过课程材料的老环境，一次性），回填上传记录的投递类型**：

```bash
psql "postgresql://postgres:密码@localhost:5432/narra" -f migrations/0010_backfill_upload_record_kind.sql
```

上传记录新增 `kind` 列（知识库文档 / 课程材料），上传时从文档快照写入。本脚本把
"关联文档是课程材料"的历史记录回填成 `material`；文档已被删除的记录无法追溯，
保持 `knowledge`。幂等，新环境跑命中 0 行。不用改 `updated_at` —— kind 是补充的
身份信息，不是状态变更。

**第十二步（视觉改造 v2，换任何环境都要跑一次），圆桌六角色降饱和配色**：

```bash
psql "postgresql://postgres:密码@localhost:5432/narra" -f migrations/0011_desaturate_role_colors.sql
```

六个预设角色的配色从饱和度 64%~96% 降到 20%~44%，与新设计系统的主色（雾霾蓝
`#46639c`，38%）统一。理由：六个人围在圆桌一圈时，高饱和色会互相抢注意力，课件
内容反而被衬得看不清。降饱和后六人仍可互相区分，界面安静下来。

- **幂等**，只替换六个角色的旧默认色；重复执行为 0 行。用户自建角色与已手工修改的预设角色颜色不受影响。
- **新环境也要跑**：0002 种子里的仍是旧配色，seed 不会因为本脚本而改变。
- **不用改 `avatar` 字段**：新头像是同名替换文件内容，文件名和路径都没变。
- 脚本注释末尾附了回滚 SQL。

| 角色 | 旧 | 新 |
| --- | --- | --- |
| 陈老师 | `#722ed1` | `#46639c` |
| 小助手 | `#13c2c2` | `#4f8a7b` |
| 气氛组 | `#fa8c16` | `#b8894a` |
| 好奇宝宝 | `#52c41a` | `#8a9a4f` |
| 笔记君 | `#2f54eb` | `#7a6f9e` |
| 杠精同学 | `#eb2f96` | `#a8697a` |


## 约束全部归实体 tag（2026-09-17 大迁移）

历史上约束分两处：AutoMigrate 建表和单列 UNIQUE，SQL 文件补 CHECK / 外键 /
复合 UNIQUE / 索引（`0001` / `0002_knowledge_base` / `0003_*` / `0004_llm_providers`
共 5 个文件，约 115 个对象）。2026-09-17 起**全部迁入实体 tag**，那 5 个文件已删除
（git 历史可查）；当天稍后又把最后一个留在 SQL 里的结构对象——
`0004_knowledge_document_status.sql` 的队列部分索引——也搬进了 tag
（关键是转义逗号，见下）。**至此 `migrations/` 不再含任何表结构 DDL**，
只剩种子数据与历史清理。
依据是 GORM v1.31.2 的这些能力都核对过源码：

| 对象 | tag 写法 | 说明 |
|---|---|---|
| CHECK | `check:约束名,表达式` | 表达式里的逗号会被还原；**每字段限一条**，跨列表达式挂在任一相关字段上 |
| 外键 | belongs-to 指针字段 + `constraint:约束名,OnDelete:CASCADE|SET NULL|RESTRICT` | 名字与库里现有约束逐字对齐，存量库 `HasConstraint` 命中直接跳过 |
| 复合 UNIQUE | 各字段同名的 `uniqueIndex:约束名` | 建的是唯一索引，与唯一约束在 PG 里等价 |
| 部分唯一索引 | `uniqueIndex:名,where:谓词` | 安全：`field.Unique` 只由 `unique` tag 置位，`MigrateColumnUnique` 不管 uniqueIndex |
| 部分索引 | `index:名,where:谓词` | 谓词含逗号时必须转义，且要写**双反斜杠** `\\,`，见下 |
| GIN 索引 | `index:名,type:gin` | |
| 排序/列序 | `sort:DESC`、`priority:n` | 同 priority 按字段声明顺序 |
| 同字段多条索引 | 重复写多段 `index:` / `uniqueIndex:` | tag 解析按分号逐段处理，不是覆盖 |
| 列默认值 | `default:表达式` | 含函数/INTERVAL 的表达式 PG 会规范化存储，AutoMigrate 可能每次启动重发一次 `ALTER COLUMN SET DEFAULT`，幂等无害 |

### tag 里的逗号：两种规则，都不直观

**`check:` 不用转义。** `ParseCheckConstraints` 取第一个逗号前为约束名，剩下的用
`strings.Join(names[1:], ",")` 拼回表达式，所以 `check:名,状态 IN ('a', 'b')` 可以直接写。

**`index:` / `uniqueIndex:` 的 `where:` 必须转义，而且要写双反斜杠。** 两层机制叠加：

1. `reflect.StructTag` 用 `strconv.Unquote` 解码 tag 值。`\,` **不是合法的 Go 转义
   序列**，会让 `Tag.Get("gorm")` 直接失败返回空串——**整条 tag 被静默丢弃**，字段上
   的 type / not null / index 全部消失，**不报错**。
2. GORM 的 `ParseTagSetting`（`schema/utils.go`）支持反斜杠续行：段尾是 `\` 时与下一段
   合并，于是 `\,` 被还原成 `,`。

所以源码里要写 `\\,`，链路是：**源码 `\\,` → Unquote 得 `\,` → GORM 还原成 `,`**。

```go
// 正确：生成 WHERE status IN ('pending','processing')
CreatedAt time.Time `gorm:"...;index:knowledge_documents_status_created_at_idx,where:status IN ('pending'\\,'processing')"`

// 错误：单个反斜杠会让整条 tag 被 Unquote 丢弃，索引静默消失
CreatedAt time.Time `gorm:"...;index:knowledge_documents_status_created_at_idx,where:status IN ('pending'\,'processing')"`
```

谓词不含逗号时（如 `where:enabled`、`where:deleted_at IS NULL`）不需要任何转义。
改动带 `where:` 的 tag 后，务必核对 `pg_indexes` 里索引还在、定义没变。

**第五步，更新讲稿 ready 约束**：

```bash
psql "postgresql://postgres:密码@localhost:5432/narra" -f migrations/0004_scene_segments_ready_constraint.sql
```

`0004` 将 `scene_segments` 的 `ready` 约束从“必须有音频”调整为“有音频或有讲稿文本”。
这样 TTS 未启用时，讲稿仍可以标记为 `ready`，而 `audio_path` 保持为空。

两个配套的实体写法约定：

- **关联字段只是外键的载体**。所有 belongs-to 关联都是指针类型、零值 nil、
  `json:"-"`，业务代码**禁止赋值或 Preload**——赋了非空值再 `Save`，
  GORM 会连带 upsert 目标表的行。
- **遮蔽 BaseModel 字段挂索引**。索引列是 `created_at` / `updated_at` 时
  （如 `idx_orchestration_runs_recent`、`knowledge_documents_enabled_updated_at_idx`），
  在子结构里重新声明同名字段并带上完整的 `autoCreateTime` / `autoUpdateTime` tag。
  GORM schema 去重时直接声明的字段 BindNames 更短，会覆盖嵌入字段，行为不变。

`DisableForeignKeyConstraintWhenMigrating` 已从 `pkg/database/postgres.go` 移除，
**不要加回来**——加回来全新环境就只剩表和列，外键全缺。

### 同一条约束仍然只能有一处声明

AutoMigrate 每次启动都会对账：库里唯一而实体上没标 `unique` → 判定多余 → 按
`uni_<表>_<列>` 去删，删不掉直接 panic（`MigrateColumnUnique`）。所以
**单列唯一约束永远只在实体上写 `unique` tag，不要再建任何 SQL 文件来声明它**。

## updated_at 由 GORM 维护，不挂触发器

**`updated_at` 没有任何数据库触发器，全部由 GORM 的 `autoUpdateTime` 写入**
（`internal/model/entity/base.go` 的 `UpdatedAt` 字段 tag）。

2026-09-17 之前，每张表都挂着一个 `trg_<表>_updated_at` 触发器，调用全库共用的
`set_updated_at()` 函数，在 `BEFORE UPDATE` 时把 `updated_at` 覆盖成数据库时钟。
这套机制已整体删除，建触发器的迁移文件也已删除——**新建的库不会再产生触发器**。
老环境若要就地清理残留，用上面「执行顺序」第六步那段 SQL。

**代价 —— 这一点需要知道**：`updated_at` 的可靠性从「数据库保证」降级为「每个写代码
的人都要记得」。下面这些写法不会再刷新它，而且**不报错**：

| 写入方式 | 刷新 `updated_at` |
|---|---|
| GORM `Save` / `Update` / `Updates(struct)` | ✅ |
| GORM `UpdateColumn` / `UpdateColumns` | ❌ |
| GORM `Omit("updated_at")`，或 `Select` 白名单不含它 | ❌ |
| 裸 `db.Exec("UPDATE ...")` | ❌ |
| `psql` 手工改库、数据修复脚本、外部工具 | ❌ |

将来若加裸 UPDATE（如僵尸状态清理），记得自己带上 `updated_at = now()`，或者改用
GORM 的 API。

> 顺带记住：`created_at` 从来没有触发器保护，而 GORM 的 `autoCreateTime` **只在 INSERT
> 时生效、UPDATE 时完全不碰**。所以「新组一个对象再 `Save`」这种写法会把 `created_at`
> 写成 Go 的零值（`0001-01-01`）。`embedding_settings` 第 1 行就是这么坏的。

## 改结构时

- **加字段、改类型**：改实体，重启服务，AutoMigrate 自动同步
- **删字段**：AutoMigrate 只加不减，要手动 `DROP COLUMN`
- **加 CHECK / 唯一 / 索引 / 外键**：改实体 tag（写法见上表），重启服务
- **改状态值**：实体常量和对应字段上的 check tag，**两处一起改**
- ⚠️ **收紧已存在的 CHECK**（从取值集合里删值）：AutoMigrate 对已存在的表只在
  `HasConstraint` 为假时才建约束，**不会替换**已有的 CHECK。实体 tag 改完后，
  存量库要手动 `DROP CONSTRAINT` 再重启服务（或同一条 SQL 里 `ADD CONSTRAINT`），
  新环境由 tag 直接建出收紧后的约束。2026-09-25 删 `source_type = 'api'` 时踩到过这一点
  （见上面「执行顺序」第七步）
- **WHERE 谓词含逗号的索引**：照样写 tag，把逗号写成 `\\,`（见上「tag 里的逗号」），
  不要新建 SQL 文件
- **想加触发器**：**先别加。** `updated_at` 由 GORM 维护，见上面那节

工作台的三张表 V1 不建，见设计文档 `docs/database-design-v1.md` §9.5。
