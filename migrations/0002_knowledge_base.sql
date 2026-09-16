-- 0002_knowledge_base.sql
-- Global RAG knowledge base. Run after 0001_constraints.sql.

BEGIN;

CREATE EXTENSION IF NOT EXISTS vector;

CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

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
    enabled boolean NOT NULL DEFAULT true,
    -- UNIQUE (name) 由实体字段上的 unique tag 声明，AutoMigrate 负责建。单列唯一约束写在这里
    -- 会让 AutoMigrate 每次启动都去删它自己算出来的名字（uni_embedding_models_name），删不掉就 panic。
    -- 见 migrations/README.md。
    CONSTRAINT embedding_models_dimensions_check CHECK (dimensions > 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS embedding_models_one_default_idx
    ON embedding_models (is_default) WHERE is_default;

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
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT knowledge_documents_source_type_check
        CHECK (source_type IN ('manual', 'import', 'api'))
);

CREATE INDEX IF NOT EXISTS knowledge_documents_enabled_updated_at_idx
    ON knowledge_documents (updated_at DESC) WHERE enabled;
CREATE INDEX IF NOT EXISTS knowledge_documents_metadata_idx
    ON knowledge_documents USING gin (metadata);

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
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT knowledge_chunks_document_id_chunk_index_key UNIQUE (document_id, chunk_index),
    CONSTRAINT knowledge_chunks_document_id_fkey
        FOREIGN KEY (document_id) REFERENCES knowledge_documents (id) ON DELETE CASCADE,
    CONSTRAINT knowledge_chunks_chunk_index_check CHECK (chunk_index >= 0),
    CONSTRAINT knowledge_chunks_character_count_check CHECK (character_count > 0),
    CONSTRAINT knowledge_chunks_token_count_check CHECK (token_count IS NULL OR token_count > 0)
);

CREATE INDEX IF NOT EXISTS knowledge_chunks_document_id_idx
    ON knowledge_chunks (document_id, chunk_index);
CREATE INDEX IF NOT EXISTS knowledge_chunks_metadata_idx
    ON knowledge_chunks USING gin (metadata);

CREATE TABLE IF NOT EXISTS knowledge_embeddings (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    chunk_id bigint NOT NULL,
    model_id bigint NOT NULL,
    dimensions integer NOT NULL,
    embedding vector NOT NULL,
    generated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT knowledge_embeddings_chunk_id_model_id_key UNIQUE (chunk_id, model_id),
    CONSTRAINT knowledge_embeddings_chunk_id_fkey
        FOREIGN KEY (chunk_id) REFERENCES knowledge_chunks (id) ON DELETE CASCADE,
    CONSTRAINT knowledge_embeddings_model_id_fkey
        FOREIGN KEY (model_id) REFERENCES embedding_models (id) ON DELETE RESTRICT,
    CONSTRAINT knowledge_embeddings_dimensions_check CHECK (dimensions > 0),
    CONSTRAINT knowledge_embeddings_vector_dimensions_check CHECK (vector_dims(embedding) = dimensions)
);

CREATE INDEX IF NOT EXISTS knowledge_embeddings_model_id_idx
    ON knowledge_embeddings (model_id);
CREATE INDEX IF NOT EXISTS knowledge_embeddings_chunk_id_idx
    ON knowledge_embeddings (chunk_id);

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
