-- 0002_seed_preset_agents.sql
-- 角色池的初始数据：原来写死在 internal/agent/role.go 的那 6 个角色。
--
--   psql "postgresql://postgres:密码@localhost:5432/narra" -f migrations/0002_seed_preset_agents.sql
--
-- 幂等：ON CONFLICT (agent_key) DO NOTHING。这是**初始值**，不是权威值——
-- 角色池归人工维护，以后加角色、改角色直接改库，不用回改这里，重复跑也不会覆盖。
-- 所以它单独一个文件，跟结构迁移（0001）分开。
--
-- sort_order 就是前端角色列表的展示顺序：0 是教师，其余按原来的圆桌顺序排。
-- 这个顺序也是圆桌上角色的排列顺序——classroom_agents 不存位次。
--
-- voice_id 必须逐字命中 internal/agent/voice.go 的音色目录，写错了读取角色池时会被拦下。
-- avatar 指向 frontend/public/avatars 下的文件，数据库只校验长度，拦不住编造的文件名。

BEGIN;

INSERT INTO preset_agents
    (agent_key, name, role, role_type, persona, avatar, color, voice_id, sort_order, enabled)
VALUES
    ('teacher', '陈老师', '主讲', 'teacher',
     '主讲老师，负责整体讲解与节奏把控，会把零散知识点串成体系，并在讨论里引导方向、把握深浅。',
     '/avatars/teacher-2.png', '#722ed1', 'voxcpm-zh-female-warm', 0, true),

    ('assist', '小助手', '辅助', 'assistant',
     '老师的得力助手，负责补充细节、梳理步骤，并在同学卡顿时给出恰到好处的分步提示。',
     '/avatars/assist-2.png', '#13c2c2', 'voxcpm-zh-female-clear', 1, true),

    ('clown', '气氛组', '吐槽', 'student',
     '课堂里的开心果，擅长用生活化的类比把抽象概念讲得妙趣横生，也会适时活跃气氛、化解冷场。',
     '/avatars/clown-2.png', '#fa8c16', 'voxcpm-zh-male-lively', 2, true),

    ('curious', '好奇宝宝', '提问', 'student',
     '永远在问“为什么”的那一个，喜欢追问概念的边界和例外情况，往往把讨论推向更深入。',
     '/avatars/curious-2.png', '#52c41a', 'voxcpm-zh-male-young', 3, true),

    ('note-taker', '笔记君', '记录', 'student',
     '认真的记录者，会把零散的要点整理成结构清晰的脉络图，方便同学回看和复习。',
     '/avatars/note-taker-2.png', '#2f54eb', 'voxcpm-zh-female-calm', 4, true),

    ('thinker', '杠精同学', '质疑', 'student',
     '习惯从反例与边界条件切入思考，喜欢质疑表面结论，锻炼大家的严谨性。',
     '/avatars/thinker-2.png', '#eb2f96', 'voxcpm-zh-male-deep', 5, true)

ON CONFLICT (agent_key) DO NOTHING;

COMMIT;
