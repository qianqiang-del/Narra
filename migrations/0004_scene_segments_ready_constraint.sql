-- 0004_scene_segments_ready_constraint.sql
--
-- scene_segments.status = 'ready' now means that the narration text is ready.
-- Audio is optional when TTS is disabled, so audio_path may be NULL.
--
-- Safe to run repeatedly.

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
