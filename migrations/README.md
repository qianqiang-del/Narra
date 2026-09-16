# 数据库迁移

V1 六张表：`folders`、`classrooms`、`preset_agents`、`classroom_agents`、`scenes`、
`scene_segments`。

其中 `preset_agents` 是**角色池**（人工维护的角色清单），`classroom_agents` 是
**课堂与角色的关联表**——它只存 `classroom_id` / `agent_id` / `voice_id`，
角色的名称、人设、头像都去 `preset_agents` 查。

## 三步

**第一步，建表** —— 启动服务时自动跑（`internal/app/app.go` 里 `initDatabase` 的那句
`AutoMigrate`），也可以手动：

```bash
go run ./cmd/server -c configs/config.yaml
```

**第二步，补约束** —— 这一步不能省：

```bash
psql "postgresql://postgres:密码@localhost:5432/narra" -f migrations/0001_constraints.sql
```

`0001_constraints.sql` 是幂等的，可以反复跑。

**第三步，灌角色池初始数据**（只需要跑一次）：

```bash
psql "postgresql://postgres:密码@localhost:5432/narra" -f migrations/0002_seed_preset_agents.sql
```

`0002` 也是幂等的（`ON CONFLICT DO NOTHING`），但它灌的是**初始值**，不是权威值：
角色池以后由人工直接维护数据库，加角色、改角色不用回改这个文件。

**第四步，补 Embedding 配置约束**：

```bash
psql "postgresql://postgres:密码@localhost:5432/narra" -f migrations/0003_embedding_settings.sql
```

`embedding_settings` 允许保存多条 OpenAI 兼容服务配置，但数据库会限制同时只有一条当前启用配置。

## 谁拥有约束

**GORM 表达得出的归 GORM，表达不出的归 SQL。同一条约束只能有一处声明。**

| 归 AutoMigrate（实体 tag 声明） | 归 `0001_constraints.sql` |
|---|---|
| 表、列、类型 | CHECK 约束 |
| 单列 UNIQUE（字段上的 `unique` tag） | 复合 UNIQUE |
|  | 外键 |
|  | 触发器 |
|  | 外键索引 |

GORM 建不出来的那几样，原因各不相同：CHECK 在 GORM tag 里根本表达不了；外键靠关联字段
推导，而实体故意不加关联字段（免得查询时被意外预加载）；触发器不归它管；外键索引 GORM
不建，PostgreSQL 也不会为外键自动建（MySQL 会，容易误以为这里有）。

**不跑第二步的后果**：删课程不会级联删场景，外键索引也没有。

### 两边都声明会炸，而且有两种炸法

- **名字不同** → AutoMigrate 找不到要删的约束，返回 error，服务启动直接 panic
- **名字恰好相同** → 它删成功，约束静默消失，没有任何提示

原因在 GORM 的 `MigrateColumnUnique`（`gorm/migrator/migrator.go`）。每次启动它都对账一次：
「库里这列唯一、实体上没标 `unique`」→ 判定这条约束是多余的 → 删。而删的时候，名字是
`NamingStrategy.UniqueName` **现算**出来的 `uni_<表>_<列>`，**不去读真实约束名**。

**复合唯一约束不会被盯上**：复合约束不会让其中单个列的 `columnType.Unique()` 变成 true，
所以 `classroom_agents (classroom_id, agent_id)` 这类是安全的。

这条是踩出来的：`preset_agents` 的 `agent_key` / `sort_order` 一开始写在 `0001` 里，
`0001` 跑完之后**每次启动都 panic**。

## 改结构时

- **加字段、改类型**：改实体，重启服务，AutoMigrate 自动同步
- **删字段**：AutoMigrate 只加不减，要手动 `DROP COLUMN`
- **加单列唯一约束**：在实体字段上加 `unique` tag，**不要**写进 `0001`
- **加别的约束**：写进 `0001_constraints.sql`，保持幂等
- **改状态值**：实体常量和 `0001_constraints.sql` 里的 CHECK，**两处一起改**

工作台的三张表 V1 不建，见设计文档 `docs/database-design-v1.md` §9.5。
