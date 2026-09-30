-- 0007_drop_embedding_model_dead_columns.sql
--
-- 一次性删除 embedding_models 上四个无人读写的冗余列，只对 2026-09-30 之前建的
-- 老环境有意义；全新环境由实体 tag 直接建出精简后的表，跑本脚本也是 0 影响
-- （幂等，可重复执行）。AutoMigrate 只加列不减列，所以存量库必须手动删。
--
-- 删除理由（查过全部引用后的结论）：
--   provider      —— 固定写 openai-compatible 且从未被读取；协议类型属于"怎么连服务"，
--                    只归 embedding_settings.provider。
--   base_url      —— 每次登记都由 EnsureDefault 从 embedding_settings 复制，没有任何
--                    入口能单独改它；"连哪个网关"归 embedding_settings.base_url。
--   model_version —— 上游从未赋过值（永远 NULL），也从未被读取。
--   enabled       —— EnsureDefault 写死 true，全项目没有读取。
--
-- ⚠️ 顺序要求：**停掉旧版服务 → 执行本脚本 → 再启动新版服务**。
-- 两边各有一条硬约束，缺一不可：
--   * 只删列、不升级代码：旧版代码仍会向这四列写入（登记默认模型时），
--     会报 "column does not exist"；
--   * 只升级代码、不跑本脚本：新版不再写 provider / enabled，而它们在旧表上是
--     NOT NULL 且没有默认值，登记模型时直接违反非空约束（设置页保存、启动对齐
--     都会失败）。AutoMigrate 不会替存量库删列，所以迁移必须手动跑。
-- 也就是说：本脚本与新版代码要一起上，先跑脚本、后启新版。
--
-- Windows 上先设 PGCLIENTENCODING=UTF8 再跑（脚本注释是中文，psql 默认按客户端
-- locale 解码，GBK 环境会直接报编码错误、整条命令中止）：
--   PGCLIENTENCODING=UTF8 psql "postgresql://postgres:密码@localhost:5432/narra" -f migrations/0007_drop_embedding_model_dead_columns.sql

BEGIN;

ALTER TABLE embedding_models
    DROP COLUMN IF EXISTS provider;

ALTER TABLE embedding_models
    DROP COLUMN IF EXISTS base_url;

ALTER TABLE embedding_models
    DROP COLUMN IF EXISTS model_version;

ALTER TABLE embedding_models
    DROP COLUMN IF EXISTS enabled;

COMMIT;

-- 跑完后可选自检：
--   \d embedding_models
--     应只剩 id / created_at / updated_at / name / dimensions / is_default 共 6 列；
--   SELECT indexdef FROM pg_indexes WHERE tablename = 'embedding_models';
--     应仍有 uni_embedding_models_name 与 embedding_models_one_default_idx。
