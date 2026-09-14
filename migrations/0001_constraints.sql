-- 0001_constraints.sql
-- AutoMigrate 建完表之后跑这个，补上它建不出来的东西：唯一约束、CHECK、外键、updated_at 触发器。
-- 幂等，可以反复跑。
--
--   psql "postgresql://postgres:密码@localhost:5432/narra" -f migrations/0001_constraints.sql
--
-- 改状态值时记得两边一起改：实体里的常量，和这里的 CHECK。

BEGIN;

CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;


-- folders
DROP TRIGGER IF EXISTS trg_folders_updated_at ON folders;
CREATE TRIGGER trg_folders_updated_at
    BEFORE UPDATE ON folders
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- classrooms
ALTER TABLE classrooms DROP CONSTRAINT IF EXISTS classrooms_mode_check;
ALTER TABLE classrooms ADD CONSTRAINT classrooms_mode_check
    CHECK (mode IN ('vocational', 'interactive'));

ALTER TABLE classrooms DROP CONSTRAINT IF EXISTS classrooms_status_check;
ALTER TABLE classrooms ADD CONSTRAINT classrooms_status_check
    CHECK (status IN ('generating', 'playable', 'ready', 'failed'));

ALTER TABLE classrooms DROP CONSTRAINT IF EXISTS classrooms_folder_id_fkey;
ALTER TABLE classrooms ADD CONSTRAINT classrooms_folder_id_fkey
    FOREIGN KEY (folder_id) REFERENCES folders (id) ON DELETE SET NULL;

DROP TRIGGER IF EXISTS trg_classrooms_updated_at ON classrooms;
CREATE TRIGGER trg_classrooms_updated_at
    BEFORE UPDATE ON classrooms
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- classroom_agents
ALTER TABLE classroom_agents DROP CONSTRAINT IF EXISTS classroom_agents_source_check;
ALTER TABLE classroom_agents ADD CONSTRAINT classroom_agents_source_check
    CHECK (source IN ('preset', 'auto'));

ALTER TABLE classroom_agents DROP CONSTRAINT IF EXISTS classroom_agents_role_type_check;
ALTER TABLE classroom_agents ADD CONSTRAINT classroom_agents_role_type_check
    CHECK (role_type IN ('teacher', 'assistant', 'student'));

ALTER TABLE classroom_agents DROP CONSTRAINT IF EXISTS classroom_agents_agent_key_check;
ALTER TABLE classroom_agents ADD CONSTRAINT classroom_agents_agent_key_check
    CHECK (agent_key IN ('teacher', 'assist', 'clown', 'curious', 'note-taker', 'thinker'));

ALTER TABLE classroom_agents DROP CONSTRAINT IF EXISTS classroom_agents_sort_order_check;
ALTER TABLE classroom_agents ADD CONSTRAINT classroom_agents_sort_order_check
    CHECK (sort_order IS NULL OR sort_order >= 0);

ALTER TABLE classroom_agents DROP CONSTRAINT IF EXISTS classroom_agents_classroom_id_sort_order_key;
ALTER TABLE classroom_agents ADD CONSTRAINT classroom_agents_classroom_id_sort_order_key
    UNIQUE (classroom_id, sort_order);

ALTER TABLE classroom_agents DROP CONSTRAINT IF EXISTS classroom_agents_classroom_id_agent_key_key;
ALTER TABLE classroom_agents ADD CONSTRAINT classroom_agents_classroom_id_agent_key_key
    UNIQUE (classroom_id, agent_key);

ALTER TABLE classroom_agents DROP CONSTRAINT IF EXISTS classroom_agents_classroom_id_fkey;
ALTER TABLE classroom_agents ADD CONSTRAINT classroom_agents_classroom_id_fkey
    FOREIGN KEY (classroom_id) REFERENCES classrooms (id) ON DELETE CASCADE;

DROP TRIGGER IF EXISTS trg_classroom_agents_updated_at ON classroom_agents;
CREATE TRIGGER trg_classroom_agents_updated_at
    BEFORE UPDATE ON classroom_agents
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- scenes
ALTER TABLE scenes DROP CONSTRAINT IF EXISTS scenes_type_check;
ALTER TABLE scenes ADD CONSTRAINT scenes_type_check
    CHECK (type IN ('slide', 'quiz', 'interactive', 'pbl', 'complete'));

ALTER TABLE scenes DROP CONSTRAINT IF EXISTS scenes_status_check;
ALTER TABLE scenes ADD CONSTRAINT scenes_status_check
    CHECK (status IN ('pending', 'generating', 'ready', 'failed'));

ALTER TABLE scenes DROP CONSTRAINT IF EXISTS scenes_sort_order_check;
ALTER TABLE scenes ADD CONSTRAINT scenes_sort_order_check
    CHECK (sort_order >= 0);

ALTER TABLE scenes DROP CONSTRAINT IF EXISTS scenes_classroom_id_sort_order_key;
ALTER TABLE scenes ADD CONSTRAINT scenes_classroom_id_sort_order_key
    UNIQUE (classroom_id, sort_order);

ALTER TABLE scenes DROP CONSTRAINT IF EXISTS scenes_classroom_id_fkey;
ALTER TABLE scenes ADD CONSTRAINT scenes_classroom_id_fkey
    FOREIGN KEY (classroom_id) REFERENCES classrooms (id) ON DELETE CASCADE;

DROP TRIGGER IF EXISTS trg_scenes_updated_at ON scenes;
CREATE TRIGGER trg_scenes_updated_at
    BEFORE UPDATE ON scenes
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- scene_segments
ALTER TABLE scene_segments DROP CONSTRAINT IF EXISTS scene_segments_status_check;
ALTER TABLE scene_segments ADD CONSTRAINT scene_segments_status_check
    CHECK (status IN ('pending', 'generating', 'ready', 'failed'));

ALTER TABLE scene_segments DROP CONSTRAINT IF EXISTS scene_segments_sort_order_check;
ALTER TABLE scene_segments ADD CONSTRAINT scene_segments_sort_order_check
    CHECK (sort_order >= 0);

ALTER TABLE scene_segments DROP CONSTRAINT IF EXISTS scene_segments_ready_has_audio_check;
ALTER TABLE scene_segments ADD CONSTRAINT scene_segments_ready_has_audio_check
    CHECK (status <> 'ready' OR audio_path IS NOT NULL);

ALTER TABLE scene_segments DROP CONSTRAINT IF EXISTS scene_segments_scene_id_content_key_key;
ALTER TABLE scene_segments ADD CONSTRAINT scene_segments_scene_id_content_key_key
    UNIQUE (scene_id, content_key);

ALTER TABLE scene_segments DROP CONSTRAINT IF EXISTS scene_segments_scene_id_sort_order_key;
ALTER TABLE scene_segments ADD CONSTRAINT scene_segments_scene_id_sort_order_key
    UNIQUE (scene_id, sort_order);

ALTER TABLE scene_segments DROP CONSTRAINT IF EXISTS scene_segments_scene_id_fkey;
ALTER TABLE scene_segments ADD CONSTRAINT scene_segments_scene_id_fkey
    FOREIGN KEY (scene_id) REFERENCES scenes (id) ON DELETE CASCADE;

DROP TRIGGER IF EXISTS trg_scene_segments_updated_at ON scene_segments;
CREATE TRIGGER trg_scene_segments_updated_at
    BEFORE UPDATE ON scene_segments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMIT;
