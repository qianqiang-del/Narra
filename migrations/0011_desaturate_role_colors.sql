-- 0011_desaturate_role_colors.sql
--
-- 圆桌六个预设角色的配色从高饱和降到低饱和，让它们和新设计系统的主色
-- （雾霾蓝 #46639c，饱和度 38%）处在同一个光圈里。
--
-- 为什么不只是"调好看一点"：原配色饱和度在 64%~96% 之间（气氛组 #fa8c16 是 96%，
-- 几乎是纯色）。六个人围在圆桌一圈时高饱和色会互相抢注意力，课件内容反而被
-- 衬得看不清 —— 界面比内容更跳。降到 20%~44% 之后，六人仍然互相可辨，但界面
-- 安静下来，注意力回到内容上。
--
-- 各角色的降幅是按人设定的，不是一刀切：陈老师降幅最小（26%）因为它是主讲，
-- 需要保留一点存在感；杠精同学降幅最大（56%）因为原色是亮粉，留在圆桌里最跳。
--
-- 幂等：只替换六个角色的旧默认色，重复执行为 0 行。用户自建角色和
-- 后来手工修改过的预设角色颜色都不受影响。
--
-- 刻意不动 avatar：新头像是「同名替换文件内容」，文件名和路径都没变
-- （public/avatars/teacher-2.png 等），所以数据库不需要跟着改。
--
-- 新环境也要跑这一条：0002 种子里的仍是旧配色，seed 不会因为本脚本而改变。
--
-- 回滚（如果不喜欢这套配色）：
--   UPDATE preset_agents SET color = '#722ed1' WHERE agent_key = 'teacher' AND color = '#46639c';
--   UPDATE preset_agents SET color = '#13c2c2' WHERE agent_key = 'assist' AND color = '#4f8a7b';
--   UPDATE preset_agents SET color = '#fa8c16' WHERE agent_key = 'clown' AND color = '#b8894a';
--   UPDATE preset_agents SET color = '#52c41a' WHERE agent_key = 'curious' AND color = '#8a9a4f';
--   UPDATE preset_agents SET color = '#2f54eb' WHERE agent_key = 'note-taker' AND color = '#7a6f9e';
--   UPDATE preset_agents SET color = '#eb2f96' WHERE agent_key = 'thinker' AND color = '#a8697a';

BEGIN;

UPDATE preset_agents SET color = '#46639c' WHERE agent_key = 'teacher'    AND color = '#722ed1';
UPDATE preset_agents SET color = '#4f8a7b' WHERE agent_key = 'assist'     AND color = '#13c2c2';
UPDATE preset_agents SET color = '#b8894a' WHERE agent_key = 'clown'      AND color = '#fa8c16';
UPDATE preset_agents SET color = '#8a9a4f' WHERE agent_key = 'curious'    AND color = '#52c41a';
UPDATE preset_agents SET color = '#7a6f9e' WHERE agent_key = 'note-taker' AND color = '#2f54eb';
UPDATE preset_agents SET color = '#a8697a' WHERE agent_key = 'thinker'    AND color = '#eb2f96';

COMMIT;
