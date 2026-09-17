BEGIN;

ALTER TABLE llm_providers DROP CONSTRAINT IF EXISTS llm_providers_protocol_check;
ALTER TABLE llm_providers ADD CONSTRAINT llm_providers_protocol_check
    CHECK (protocol = 'openai-compatible');

ALTER TABLE llm_providers DROP CONSTRAINT IF EXISTS llm_providers_timeout_check;
ALTER TABLE llm_providers ADD CONSTRAINT llm_providers_timeout_check
    CHECK (timeout_seconds BETWEEN 1 AND 600);

ALTER TABLE llm_providers DROP CONSTRAINT IF EXISTS llm_providers_test_status_check;
ALTER TABLE llm_providers ADD CONSTRAINT llm_providers_test_status_check
    CHECK (test_status IN ('untested', 'success', 'failed'));

ALTER TABLE llm_providers DROP CONSTRAINT IF EXISTS llm_providers_models_check;
ALTER TABLE llm_providers ADD CONSTRAINT llm_providers_models_check
    CHECK (jsonb_typeof(models) = 'array' AND jsonb_array_length(models) > 0);

DROP TRIGGER IF EXISTS trg_llm_providers_updated_at ON llm_providers;
CREATE TRIGGER trg_llm_providers_updated_at
    BEFORE UPDATE ON llm_providers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMIT;
