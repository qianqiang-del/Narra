-- 0004_scene_segments_ready_constraint.sql
--
-- scene_segments.status = 'ready' 现在表示「旁白文本已就绪」。
-- 关闭 TTS 时音频是可选的，因此 audio_path 可以为 NULL。
--
-- 可安全重复执行。

ALTER TABLE scene_segments
    DROP CONSTRAINT IF EXISTS scene_segments_ready_has_audio_check;

ALTER TABLE scene_segments
    DROP CONSTRAINT IF EXISTS scene_segments_ready_has_content_check;

ALTER TABLE scene_segments
    ADD CONSTRAINT scene_segments_ready_has_content_check
    CHECK (
        status <> 'ready'
        OR audio_path IS NOT NULL
        OR text <> ''
    );
