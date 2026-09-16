-- Embedding 服务配置表的数据库约束。
-- 请在应用启动完成 AutoMigrate 后执行；可重复执行。

BEGIN;

ALTER TABLE embedding_settings DROP CONSTRAINT IF EXISTS embedding_settings_dimensions_check;
ALTER TABLE embedding_settings ADD CONSTRAINT embedding_settings_dimensions_check
    CHECK (dimensions > 0);

ALTER TABLE embedding_settings DROP CONSTRAINT IF EXISTS embedding_settings_timeout_seconds_check;
ALTER TABLE embedding_settings ADD CONSTRAINT embedding_settings_timeout_seconds_check
    CHECK (timeout_seconds > 0);

-- 多条配置可以保存，但整个系统只能有一条当前启用的配置。
CREATE UNIQUE INDEX IF NOT EXISTS embedding_settings_one_active_idx
    ON embedding_settings ((is_active))
    WHERE is_active;

DROP TRIGGER IF EXISTS trg_embedding_settings_updated_at ON embedding_settings;
CREATE TRIGGER trg_embedding_settings_updated_at
    BEFORE UPDATE ON embedding_settings
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMIT;
