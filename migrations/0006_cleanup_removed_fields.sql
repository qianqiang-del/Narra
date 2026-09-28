-- 0006_cleanup_removed_fields.sql
--
-- 一次性清理两处「实体里已经删掉、但 AutoMigrate 不会替存量库删」的库对象，
-- 只对 2026-09-25 之前建的老环境有意义；全新环境跑也是 0 影响（幂等，可重复执行）。
--
--   1. knowledge_documents.source_type 的 CHECK 去掉 'api'
--      —— 来源类型只剩 manual / import，但 AutoMigrate 只在约束不存在时创建，
--         不会替换已存在的 CHECK（见 migrations/README.md「改结构时」），要手动重建。
--   2. knowledge_chunks.token_count / metadata 两个无用列
--      —— AutoMigrate 只加列不减列，必须手动 DROP COLUMN；随列一起自动删掉的还有
--         token_count 的 CHECK（knowledge_chunks_token_count_check）和
--         metadata 上的 GIN 索引（knowledge_chunks_metadata_idx）。
--
-- 整个脚本放在一个事务里：任一步失败（比如还有 'api' 历史行）全部回滚，
-- 不会留下「约束已删、新约束没建上」或「列删了一半」的中间状态。
--
-- Windows 上先设 PGCLIENTENCODING=UTF8 再跑（脚本注释是中文，psql 默认按客户端
-- locale 解码，GBK 环境会直接报编码错误、整条命令中止）：
--   PGCLIENTENCODING=UTF8 psql "postgresql://postgres:密码@localhost:5432/narra" -f migrations/0006_cleanup_removed_fields.sql

BEGIN;

-- 1. 先挡历史行：还有 source_type = 'api' 的文档时给出可读报错，
--    而不是等 ADD CONSTRAINT 抛一句 "violates check constraint"。
--    处理方式：把这些行改成 manual 或 import（按它们的真实来源），再重跑本脚本。
DO $$
DECLARE
  api_rows bigint;
BEGIN
  SELECT count(*) INTO api_rows FROM knowledge_documents WHERE source_type = 'api';
  IF api_rows > 0 THEN
    RAISE EXCEPTION 'knowledge_documents 仍有 % 行 source_type = ''api''。排查：SELECT id, title, created_at FROM knowledge_documents WHERE source_type = ''api''；改完来源类型再重跑本脚本', api_rows;
  END IF;
END $$;

ALTER TABLE knowledge_documents
    DROP CONSTRAINT IF EXISTS knowledge_documents_source_type_check;

ALTER TABLE knowledge_documents
    ADD CONSTRAINT knowledge_documents_source_type_check
    CHECK (source_type IN ('manual', 'import'));

-- 2. 删 knowledge_chunks 的两个无用列：
--    token_count 从未被写过（永远 NULL），metadata 只在写入时被填过空对象 {}。
ALTER TABLE knowledge_chunks
    DROP COLUMN IF EXISTS token_count;

ALTER TABLE knowledge_chunks
    DROP COLUMN IF EXISTS metadata;

COMMIT;

-- 跑完后可选自检：
--   \d knowledge_chunks
--     应只剩 id / created_at / updated_at / document_id / chunk_index /
--     heading / content / character_count 共 8 列；
--   SELECT pg_get_constraintdef(oid) FROM pg_constraint
--    WHERE conname = 'knowledge_documents_source_type_check';
--     应只含 manual 与 import。
