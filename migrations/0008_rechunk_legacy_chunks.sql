-- 0008_rechunk_legacy_chunks.sql
--
-- 把用**旧切分器**收录的文档重新入队，用新切分器重建切片与向量。
-- 只对 2026-10-01 之前收录过文档的老环境有意义；全新环境（所有切片都带 section_path）
-- 跑本脚本命中 0 篇（幂等，可重复执行）。
--
-- 背景：2026-10-01 起切分改为"结构优先"（goldmark 认节树 + 按子树预算递归，
-- 见 docs/rag-database.md「切分：结构优先」）。升级只对新收录生效 ——
-- 「重新向量化」只重算向量、不重切（worker 看到切片还在就直接进 embed 阶段），
-- 所以存量文档要按下面的方式重建。
--
-- ⚠️ 顺序要求：**先用新代码重启服务 → 再执行本脚本**。
--   * 只跑脚本、服务还是旧代码：旧 worker 会用旧切法重建，等于白跑；
--   * 只升级代码、不跑本脚本：老文档维持旧切片，检索照常，但没有 section_path，
--     将来的按节去重/父子拼装用不上它。
--   psql 里还有一道列存在性检查：没重启（section_path 列还没由 AutoMigrate 建出）时
--   脚本会直接报错中止，不会误删任何数据。
--
-- 本脚本与数据修复脚本不同的一点：它只做"删旧切片 + 把文档改回 pending"，
-- 真正的重新切分与向量化由常驻 worker 在后台完成（读 documents.content → 切分 →
-- 向量化 → 落库 → ready）。因此执行前要确认两件事：
--   1. 新代码已部署且 worker 在跑；
--   2. 默认向量模型可用（否则文档会停在"失败可重试"，修好向量服务后重试即可）。
-- 重嵌入要花时间与上游额度，且文档在处理期间暂不可检索，建议低峰执行。
--
-- Windows 上先设 PGCLIENTENCODING=UTF8 再跑（脚本注释是中文，psql 默认按客户端
-- locale 解码，GBK 环境会直接报编码错误、整条命令中止）：
--   PGCLIENTENCODING=UTF8 psql "postgresql://postgres:密码@localhost:5432/narra" -f migrations/0008_rechunk_legacy_chunks.sql

\set ON_ERROR_STOP on

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'knowledge_chunks' AND column_name = 'section_path'
    ) THEN
        RAISE EXCEPTION 'section_path 列还不存在：请先用新代码重启服务（AutoMigrate 会补列），再执行本脚本';
    END IF;
END $$;

BEGIN;

-- 目标 = "ready 且存在 section_path 为空的切片"的文档。判据幂等：
-- 重建完成的文档每片都有 section_path，再跑不会重复处理。
CREATE TEMP TABLE rechunk_legacy_targets ON COMMIT DROP AS
SELECT d.id
FROM knowledge_documents d
WHERE d.status = 'ready'
  AND EXISTS (
      SELECT 1 FROM knowledge_chunks c
      WHERE c.document_id = d.id AND c.section_path IS NULL
  );

-- 先删旧切片（向量由外键级联删除）。删切片是关键：worker 开始处理前会按现实材料
-- 重算恢复点，只有"有正文、没切片"才会落到 chunk 阶段；留着切片它会直接去重算向量，
-- 切分根本不会发生。
DELETE FROM knowledge_chunks c
USING rechunk_legacy_targets t
WHERE c.document_id = t.id;

-- updated_at 没有触发器，裸 UPDATE 必须自己带上（见 migrations/README.md）。
UPDATE knowledge_documents d
SET status = 'pending',
    ingest_stage = 'chunk',
    updated_at = now()
FROM rechunk_legacy_targets t
WHERE d.id = t.id;

SELECT (SELECT count(*) FROM rechunk_legacy_targets) AS requeued_documents;

COMMIT;

-- 跑完后自查：
--   （入队即刻）pending 数应当等于上面的 requeued_documents：
--     SELECT status, count(*) FROM knowledge_documents GROUP BY status;
--   （等 worker 处理完）旧切片应清零、新切片都有节路径：
--     SELECT count(*) FROM knowledge_chunks WHERE section_path IS NULL;   -- 应为 0
--   失败的行照常是"失败可重试"形态，界面上可重试（材料是 documents.content，一直都在）。
