-- 0009_rechunk_pre_type_detection.sql
--
-- 把"文档类型判定与代码切分"（2026-10-01）改造之前切出的切片重建一遍。
-- 旧切片没有 content_type；重切之后既按新逻辑切（代码按结构、文档不变），
-- 也补上 content_type / language / symbol / symbol_type 四列。
--
-- 与 0008 的关系：0008 的判据是 section_path IS NULL，只对"有标题结构的旧切片"成立。
-- 新架构下代码与无标题文本本来就不写 section_path，再跑 0008 会反复命中同一批文档
-- （不幂等，每跑一次烧一次向量化额度）。**0008 已不适用，不要重跑。**
-- 本脚本判据换成 content_type IS NULL —— 新切法产出的每一片都带 content_type，
-- 跑完再跑命中 0 篇。
--
-- ⚠️ 顺序：**先用新代码重启服务**（AutoMigrate 补 content_type 等列），再执行本脚本。
--   * 没重启时列还不存在，脚本会直接报错中止，不会误删任何数据；
--   * 服务还是旧代码时，旧 worker 不会写 content_type，脚本会反复命中。
--
-- 重切与重嵌入由常驻 worker 在后台完成（读 documents.content → 切分 → 向量化 → ready）。
-- 期间文档暂不可检索，且要花时间与上游额度，建议低峰执行。执行前确认默认向量模型可用，
-- 否则文档会停在"失败可重试"，修好向量服务后重试即可。
--
-- Windows 上先设 PGCLIENTENCODING=UTF8 再跑（脚本注释是中文，psql 默认按客户端
-- locale 解码，GBK 环境会直接报编码错误、整条命令中止）：
--   PGCLIENTENCODING=UTF8 psql "postgresql://postgres:密码@localhost:5432/narra" -f migrations/0009_rechunk_pre_type_detection.sql

\set ON_ERROR_STOP on

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'knowledge_chunks' AND column_name = 'content_type'
    ) THEN
        RAISE EXCEPTION 'content_type 列还不存在：请先用新代码重启服务（AutoMigrate 会补列），再执行本脚本';
    END IF;
END $$;

BEGIN;

-- 目标 = "ready 且存在 content_type 为空的切片"的文档。判据幂等：
-- 重建完成的文档每片都有 content_type（document / plain_text / code），再跑不会重复处理。
CREATE TEMP TABLE rechunk_pre_type_targets ON COMMIT DROP AS
SELECT d.id
FROM knowledge_documents d
WHERE d.status = 'ready'
  AND EXISTS (
      SELECT 1 FROM knowledge_chunks c
      WHERE c.document_id = d.id AND c.content_type IS NULL
  );

-- 先删旧切片（向量由外键级联删除）。删切片是关键：worker 开始处理前会按现实材料
-- 重算恢复点，只有"有正文、没切片"才会落到 chunk 阶段；留着切片它会直接去重算向量，
-- 切分根本不会发生。
DELETE FROM knowledge_chunks c
USING rechunk_pre_type_targets t
WHERE c.document_id = t.id;

-- updated_at 没有触发器，裸 UPDATE 必须自己带上（见 migrations/README.md）。
UPDATE knowledge_documents d
SET status = 'pending',
    ingest_stage = 'chunk',
    updated_at = now()
FROM rechunk_pre_type_targets t
WHERE d.id = t.id;

SELECT (SELECT count(*) FROM rechunk_pre_type_targets) AS requeued_documents;

COMMIT;

-- 跑完后自查：
--   （入队即刻）pending 数应当等于上面的 requeued_documents：
--     SELECT status, count(*) FROM knowledge_documents GROUP BY status;
--   （等 worker 处理完）旧切片应清零：
--     SELECT count(*) FROM knowledge_chunks WHERE content_type IS NULL;   -- 应为 0
--   失败的行照常是"失败可重试"形态，界面上可重试（材料是 documents.content，一直都在）。
