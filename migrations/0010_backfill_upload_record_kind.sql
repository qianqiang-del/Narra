-- 0010_backfill_upload_record_kind.sql
--
-- 上传记录新增 kind 列（实体 tag，AutoMigrate 建列，默认 'knowledge'）之后，把
-- "投递成果是课程材料"的历史记录回填成 material。判据是关联文档的 kind：
-- 文档还在就能追溯；文档已被删除的记录无法判断，保持 knowledge（那是材料被清理后
-- 留下的历史，数量极少，不影响功能）。
--
-- 幂等：只改 kind <> 'material' 且关联文档为 material 的行，重复执行为 0 行。
-- 新环境不用跑（没有历史材料记录）。
--
-- 刻意不动 updated_at：kind 是补充的身份信息，不是一次状态变更；记录抽屉按
-- created_at 倒序，也不依赖 updated_at（与 0004 回填时同一条理由）。

UPDATE knowledge_upload_records AS r
SET kind = 'material'
FROM knowledge_documents AS d
WHERE r.document_id = d.id
  AND d.kind = 'material'
  AND r.kind <> 'material';
