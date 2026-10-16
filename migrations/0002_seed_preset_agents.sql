-- Run in the narra database after the API has created users and preset_agents.
-- Seeds existing users and installs a trigger for users registered later.
-- Re-running preserves existing roles and manual edits to them.

BEGIN;

CREATE OR REPLACE FUNCTION narra_seed_preset_agents(p_owner_id bigint)
RETURNS void LANGUAGE sql AS $$
    INSERT INTO preset_agents
        (owner_id, agent_key, name, role, role_type, persona, avatar, color, voice_id,
         sort_order, enabled, created_at, updated_at)
    SELECT p_owner_id, seed.agent_key, seed.name, seed.role, seed.role_type,
           seed.persona, seed.avatar, seed.color, seed.voice_id,
           seed.sort_order, true, now(), now()
    FROM (VALUES
        ('teacher', '陈老师', '主讲', 'teacher',
         '主讲老师，负责整体讲解与节奏把控，会把零散知识点串成体系，并在讨论里引导方向、把握深浅。',
         '/avatars/teacher-2.png', '#46639c', 'Cherry', 0),
        ('assist', '小助手', '辅助', 'assistant',
         '老师的得力助手，负责补充细节、梳理步骤，并在同学卡顿时给出恰到好处的分步提示。',
         '/avatars/assist-2.png', '#4f8a7b', 'Serena', 1),
        ('clown', '气氛组', '吐槽', 'student',
         '课堂里的开心果，擅长用生活化的类比把抽象概念讲得妙趣横生，也会适时活跃气氛、化解冷场。',
         '/avatars/clown-2.png', '#b8894a', 'Nofish', 2),
        ('curious', '好奇宝宝', '提问', 'student',
         '永远在问“为什么”的那一个，喜欢追问概念的边界和例外情况，往往把讨论推向更深入。',
         '/avatars/curious-2.png', '#8a9a4f', 'Pip', 3),
        ('note-taker', '笔记君', '记录', 'student',
         '认真的记录者，会把零散的要点整理成结构清晰的脉络图，方便同学回看和复习。',
         '/avatars/note-taker-2.png', '#7a6f9e', 'Chelsie', 4),
        ('thinker', '杠精同学', '质疑', 'student',
         '习惯从反例与边界条件切入思考，喜欢质疑表面结论，锻炼大家的严谨性。',
         '/avatars/thinker-2.png', '#a8697a', 'Ethan', 5)
    ) AS seed(agent_key, name, role, role_type, persona, avatar, color, voice_id, sort_order)
    ON CONFLICT (owner_id, agent_key) DO NOTHING;
$$;

CREATE OR REPLACE FUNCTION narra_seed_preset_agents_on_registration()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM narra_seed_preset_agents(NEW.id);
    RETURN NEW;
END;
$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger
        WHERE tgrelid = 'users'::regclass
          AND tgname = 'users_seed_preset_agents_after_insert'
          AND NOT tgisinternal
    ) THEN
        CREATE TRIGGER users_seed_preset_agents_after_insert
        AFTER INSERT ON users
        FOR EACH ROW EXECUTE FUNCTION narra_seed_preset_agents_on_registration();
    END IF;
END;
$$;

SELECT narra_seed_preset_agents(id) FROM users;

COMMIT;
