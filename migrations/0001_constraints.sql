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


-- preset_agents
--
-- 角色池。人工维护，两种进入课堂的方式（用户手选 / 大模型随机挑）都只从这里取。
-- 这一段必须排在 classroom_agents 之前：那张表有指向本表的外键。
ALTER TABLE preset_agents DROP CONSTRAINT IF EXISTS preset_agents_role_type_check;
ALTER TABLE preset_agents ADD CONSTRAINT preset_agents_role_type_check
    CHECK (role_type IN ('teacher', 'assistant', 'student'));

ALTER TABLE preset_agents DROP CONSTRAINT IF EXISTS preset_agents_sort_order_check;
ALTER TABLE preset_agents ADD CONSTRAINT preset_agents_sort_order_check
    CHECK (sort_order >= 0);

-- agent_key 与 sort_order 的唯一约束**不在这里**，由实体上的 unique tag 声明，
-- AutoMigrate 建，名字是 GORM 的 uni_preset_agents_agent_key / uni_preset_agents_sort_order。
--
-- 别把它们加回来。同一个约束两边都声明的话，AutoMigrate 启动时会对账：库里唯一、实体上
-- 没标 unique → 判定多余 → 去删，而它删的名字是自己算的 uni_*，跟我们建的 *_key 对不上，
-- 于是 DROP 失败、AutoMigrate 返回 error、服务起不来。反过来若名字恰好撞上，它删成功，
-- 约束就静默消失了，更糟。详见 migrations/README.md「谁拥有约束」。

DROP TRIGGER IF EXISTS trg_preset_agents_updated_at ON preset_agents;
CREATE TRIGGER trg_preset_agents_updated_at
    BEFORE UPDATE ON preset_agents
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- classroom_agents
--
-- 课堂与角色的关联表：哪堂课用了哪些角色、各自选了什么音色。
-- 角色的名称 / 定位 / 人设 / 头像 / 主题色都不在这里，去 preset_agents 查。
--
-- 注意本表没有 role_type，所以「每堂课恰好一个教师」建不出数据库约束
-- （跨表的部分唯一索引做不到），只能由应用层在生成课程时保证。
ALTER TABLE classroom_agents DROP CONSTRAINT IF EXISTS classroom_agents_classroom_id_agent_id_key;
ALTER TABLE classroom_agents ADD CONSTRAINT classroom_agents_classroom_id_agent_id_key
    UNIQUE (classroom_id, agent_id);

ALTER TABLE classroom_agents DROP CONSTRAINT IF EXISTS classroom_agents_classroom_id_fkey;
ALTER TABLE classroom_agents ADD CONSTRAINT classroom_agents_classroom_id_fkey
    FOREIGN KEY (classroom_id) REFERENCES classrooms (id) ON DELETE CASCADE;

-- RESTRICT：还被课程引用的角色删不掉。池子里下架角色应该用 preset_agents.enabled = false。
ALTER TABLE classroom_agents DROP CONSTRAINT IF EXISTS classroom_agents_agent_id_fkey;
ALTER TABLE classroom_agents ADD CONSTRAINT classroom_agents_agent_id_fkey
    FOREIGN KEY (agent_id) REFERENCES preset_agents (id) ON DELETE RESTRICT;

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


-- 外键索引
--
-- PostgreSQL 不会给外键自动建索引（MySQL 会，所以很容易误以为这里也有）。
-- 缺索引的后果是每次删除父行都要全表扫子表：删 folders 一行扫一遍 classrooms，
-- 删 preset_agents 一行扫一遍 classroom_agents。
--
-- 只补缺的两条。其余外键列已经被复合唯一约束的前缀覆盖，再单独建是重复索引：
--   classroom_agents.classroom_id  ← UNIQUE (classroom_id, agent_id)
--   scenes.classroom_id            ← UNIQUE (classroom_id, sort_order)
--   scene_segments.scene_id        ← UNIQUE (scene_id, content_key)
CREATE INDEX IF NOT EXISTS idx_classrooms_folder_id ON classrooms (folder_id);
CREATE INDEX IF NOT EXISTS idx_classroom_agents_agent_id ON classroom_agents (agent_id);

COMMIT;
