-- 0002_knowledge_base.sql
-- Global RAG knowledge base. Run after 0001_constraints.sql.
--
-- ⚠️ 约束一律写成独立的 ALTER TABLE 语句，不要写进 CREATE TABLE 内部。
--
-- 原因是表由 AutoMigrate 抢先建好，而 PostgreSQL 的 CREATE TABLE IF NOT EXISTS
-- 在表已存在时是「整条语句跳过」—— 内联的 CHECK / 外键 / 复合 UNIQUE 一个都不会生效，
-- 而且不报任何错。迁移看起来跑成功了，实际上约束全缺，是最难发现的一种失效。
-- 这个文件原先就踩了这个坑，2026-09-17 改成现在这样。详见 migrations/README.md。
--
-- 幂等，可以反复跑。
--
--   psql "postgresql://postgres:密码@localhost:5432/narra" -f migrations/0002_knowledge_base.sql

BEGIN;

CREATE EXTENSION IF NOT EXISTS vector;

CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;


-- ============================================================================
-- 建表
--
-- 这一段只对全新环境有意义：表已存在时整条跳过，约束由下面的 ALTER 负责补齐。
-- 所以这里只写列定义，不写任何 CONSTRAINT。
-- ============================================================================

CREATE TABLE IF NOT EXISTS embedding_models (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    name varchar(160) NOT NULL,
    provider varchar(80) NOT NULL DEFAULT 'openai-compatible',
    base_url text,
    dimensions integer NOT NULL,
    model_version varchar(160),
    is_default boolean NOT NULL DEFAULT false,
    enabled boolean NOT NULL DEFAULT true
    -- UNIQUE (name) 由实体字段上的 unique tag 声明，AutoMigrate 负责建。单列唯一约束写在
    -- 这里会让 AutoMigrate 每次启动都去删它自己算出来的名字（uni_embedding_models_name），
    -- 删不掉就 panic。见 migrations/README.md。
);

CREATE TABLE IF NOT EXISTS knowledge_documents (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    title varchar(300) NOT NULL,
    content text NOT NULL,
    source_type varchar(32) NOT NULL DEFAULT 'manual',
    source_uri text,
    content_checksum char(64),
    enabled boolean NOT NULL DEFAULT true,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE IF NOT EXISTS knowledge_chunks (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    document_id bigint NOT NULL,
    chunk_index integer NOT NULL,
    heading varchar(300),
    content text NOT NULL,
    character_count integer NOT NULL,
    token_count integer,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE IF NOT EXISTS knowledge_embeddings (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    chunk_id bigint NOT NULL,
    model_id bigint NOT NULL,
    dimensions integer NOT NULL,
    embedding vector NOT NULL,
    generated_at timestamptz NOT NULL DEFAULT now()
    -- 本表没有 updated_at，所以下面也不给它建 updated_at 触发器。
);


-- ============================================================================
-- 约束
--
-- 全部写成先 DROP IF EXISTS 再 ADD，保证反复执行的结果一致。
-- knowledge_documents.status 的约束不在这里，见 0004。
-- ============================================================================

-- embedding_models
ALTER TABLE embedding_models DROP CONSTRAINT IF EXISTS embedding_models_dimensions_check;
ALTER TABLE embedding_models ADD CONSTRAINT embedding_models_dimensions_check
    CHECK (dimensions > 0);

-- knowledge_documents
ALTER TABLE knowledge_documents DROP CONSTRAINT IF EXISTS knowledge_documents_source_type_check;
ALTER TABLE knowledge_documents ADD CONSTRAINT knowledge_documents_source_type_check
    CHECK (source_type IN ('manual', 'import', 'api'));

-- knowledge_chunks
--
-- 复合 UNIQUE 不会被 AutoMigrate 盯上：复合约束不会让其中单个列的 columnType.Unique()
-- 变成 true，所以这里声明是安全的（单列唯一约束才必须留给实体）。
ALTER TABLE knowledge_chunks DROP CONSTRAINT IF EXISTS knowledge_chunks_document_id_chunk_index_key;
ALTER TABLE knowledge_chunks ADD CONSTRAINT knowledge_chunks_document_id_chunk_index_key
    UNIQUE (document_id, chunk_index);

ALTER TABLE knowledge_chunks DROP CONSTRAINT IF EXISTS knowledge_chunks_document_id_fkey;
ALTER TABLE knowledge_chunks ADD CONSTRAINT knowledge_chunks_document_id_fkey
    FOREIGN KEY (document_id) REFERENCES knowledge_documents (id) ON DELETE CASCADE;

ALTER TABLE knowledge_chunks DROP CONSTRAINT IF EXISTS knowledge_chunks_chunk_index_check;
ALTER TABLE knowledge_chunks ADD CONSTRAINT knowledge_chunks_chunk_index_check
    CHECK (chunk_index >= 0);

ALTER TABLE knowledge_chunks DROP CONSTRAINT IF EXISTS knowledge_chunks_character_count_check;
ALTER TABLE knowledge_chunks ADD CONSTRAINT knowledge_chunks_character_count_check
    CHECK (character_count > 0);

ALTER TABLE knowledge_chunks DROP CONSTRAINT IF EXISTS knowledge_chunks_token_count_check;
ALTER TABLE knowledge_chunks ADD CONSTRAINT knowledge_chunks_token_count_check
    CHECK (token_count IS NULL OR token_count > 0);

-- knowledge_embeddings
ALTER TABLE knowledge_embeddings DROP CONSTRAINT IF EXISTS knowledge_embeddings_chunk_id_model_id_key;
ALTER TABLE knowledge_embeddings ADD CONSTRAINT knowledge_embeddings_chunk_id_model_id_key
    UNIQUE (chunk_id, model_id);

ALTER TABLE knowledge_embeddings DROP CONSTRAINT IF EXISTS knowledge_embeddings_chunk_id_fkey;
ALTER TABLE knowledge_embeddings ADD CONSTRAINT knowledge_embeddings_chunk_id_fkey
    FOREIGN KEY (chunk_id) REFERENCES knowledge_chunks (id) ON DELETE CASCADE;

-- RESTRICT：还被向量引用的模型删不掉。换模型要换一个名字登记成新行，
-- 让新旧向量靠 UNIQUE (chunk_id, model_id) 共存。
ALTER TABLE knowledge_embeddings DROP CONSTRAINT IF EXISTS knowledge_embeddings_model_id_fkey;
ALTER TABLE knowledge_embeddings ADD CONSTRAINT knowledge_embeddings_model_id_fkey
    FOREIGN KEY (model_id) REFERENCES embedding_models (id) ON DELETE RESTRICT;

ALTER TABLE knowledge_embeddings DROP CONSTRAINT IF EXISTS knowledge_embeddings_dimensions_check;
ALTER TABLE knowledge_embeddings ADD CONSTRAINT knowledge_embeddings_dimensions_check
    CHECK (dimensions > 0);

-- 向量长度必须与登记维度一致，否则不同维度的向量会被存进同一个 model_id。
ALTER TABLE knowledge_embeddings DROP CONSTRAINT IF EXISTS knowledge_embeddings_vector_dimensions_check;
ALTER TABLE knowledge_embeddings ADD CONSTRAINT knowledge_embeddings_vector_dimensions_check
    CHECK (vector_dims(embedding) = dimensions);


-- ============================================================================
-- 索引
-- ============================================================================

-- 全局只能有一个默认模型。部分唯一索引：只约束 is_default 为真的那些行。
CREATE UNIQUE INDEX IF NOT EXISTS embedding_models_one_default_idx
    ON embedding_models (is_default) WHERE is_default;

-- knowledge_documents
CREATE INDEX IF NOT EXISTS knowledge_documents_enabled_updated_at_idx
    ON knowledge_documents (updated_at DESC) WHERE enabled;
CREATE INDEX IF NOT EXISTS knowledge_documents_metadata_idx
    ON knowledge_documents USING gin (metadata);

-- knowledge_chunks
--
-- 下面这条与 UNIQUE (document_id, chunk_index) 的索引重复。保留是沿用原文件的行为，
-- 两者都幂等、代价只是多一份索引，不影响正确性。
CREATE INDEX IF NOT EXISTS knowledge_chunks_document_id_idx
    ON knowledge_chunks (document_id, chunk_index);
CREATE INDEX IF NOT EXISTS knowledge_chunks_metadata_idx
    ON knowledge_chunks USING gin (metadata);

-- knowledge_embeddings
CREATE INDEX IF NOT EXISTS knowledge_embeddings_model_id_idx
    ON knowledge_embeddings (model_id);
CREATE INDEX IF NOT EXISTS knowledge_embeddings_chunk_id_idx
    ON knowledge_embeddings (chunk_id);


-- ============================================================================
-- 触发器
-- ============================================================================

DROP TRIGGER IF EXISTS trg_embedding_models_updated_at ON embedding_models;
CREATE TRIGGER trg_embedding_models_updated_at
    BEFORE UPDATE ON embedding_models
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

DROP TRIGGER IF EXISTS trg_knowledge_documents_updated_at ON knowledge_documents;
CREATE TRIGGER trg_knowledge_documents_updated_at
    BEFORE UPDATE ON knowledge_documents
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

DROP TRIGGER IF EXISTS trg_knowledge_chunks_updated_at ON knowledge_chunks;
CREATE TRIGGER trg_knowledge_chunks_updated_at
    BEFORE UPDATE ON knowledge_chunks
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMIT;
