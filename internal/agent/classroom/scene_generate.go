package classroom

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"narra/internal/model/entity"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// maxModelRetry 是一次模型调用失败后的重试次数。
const maxModelRetry = 1

// maxTTSRetry 是一段讲稿合成失败后的重试次数；TTS 是纯网络 IO，抖动比模型调用多。
const maxTTSRetry = 2

// retryBackoff 是两次重试之间的固定等待。
const retryBackoff = time.Second

func generateScenes(ctx context.Context, deps Deps, classroom *entity.Classroom, config GenerationConfig) error {
	scenes, err := deps.Scenes.ListByClassroom(ctx, classroom.ID)
	if err != nil {
		return err
	}
	teacher, voice, err := classroomTeacher(ctx, deps, classroom.ID)
	if err != nil {
		return err
	}
	rt, err := newRuntime(ctx, deps, config.ProviderID, config.ModelID, false)
	if err != nil {
		return err
	}
	contentChain, err := buildContentChain(ctx, rt)
	if err != nil {
		return err
	}
	narrationChain, err := buildNarrationChain(ctx, rt)
	if err != nil {
		return err
	}
	failed := make([]string, 0)
	for _, scene := range scenes {
		if scene.Type == entity.SceneTypeComplete || scene.Status == entity.SceneStatusReady {
			continue
		}
		if err := deps.Scenes.UpdateStatus(ctx, scene.ID, entity.SceneStatusGenerating, nil); err != nil {
			return err
		}
		input := &sceneChainInput{Classroom: classroom, Scenes: scenes, Current: scene, Teacher: teacher}
		blocks, runErr := invokeWithRetryIf(ctx, maxModelRetry, retryableSceneError, func() ([]contentBlock, error) {
			generated, err := contentChain.Invoke(ctx, input)
			if err != nil {
				return nil, err
			}
			validated, validationErr := validateBlocks(generated, scene.Type)
			if validationErr != nil {
				input.ValidationError = validationErr.Error()
			}
			return validated, validationErr
		})
		if runErr == nil {
			input.Blocks = blocks
			var narration []narrationSegment
			narration, runErr = invokeWithRetryIf(ctx, maxModelRetry, retryableModelError, func() ([]narrationSegment, error) {
				items, invokeErr := narrationChain.Invoke(ctx, input)
				if invokeErr != nil {
					return nil, invokeErr
				}
				return validateNarration(items, blocks)
			})
			if runErr == nil {
				var segments []*entity.SceneSegment
				segments, runErr = persistSceneWithRetry(ctx, deps, scene.ID, blocks, narration, deps.TTS == nil)
				if runErr == nil && deps.TTS != nil {
					runErr = synthesizeSegments(ctx, deps, classroom.ID, voice, segments)
				}
			}
		}
		if runErr != nil {
			message := truncateRunes(runErr.Error(), 500)
			_ = deps.Scenes.UpdateStatus(context.WithoutCancel(ctx), scene.ID, entity.SceneStatusFailed, &message)
			failed = append(failed, scene.Title+"："+message)
			continue
		}
		if err := deps.Scenes.UpdateStatus(ctx, scene.ID, entity.SceneStatusReady, nil); err != nil {
			return err
		}
	}
	if len(failed) > 0 {
		message := truncateRunes("部分场景生成失败："+strings.Join(failed, "；"), 500)
		return deps.Classrooms.UpdateStatus(ctx, classroom.ID, entity.ClassroomStatusPlayable, &message)
	}
	return deps.Classrooms.UpdateStatus(ctx, classroom.ID, entity.ClassroomStatusReady, nil)
}

func classroomTeacher(ctx context.Context, deps Deps, classroomID uint64) (entity.PresetAgent, string, error) {
	links, err := deps.Agents.ListByClassroom(ctx, classroomID)
	if err != nil {
		return entity.PresetAgent{}, "", err
	}
	ids := make([]uint64, 0, len(links))
	for _, link := range links {
		ids = append(ids, link.AgentID)
	}
	roles, err := deps.Roles.ListByIDs(ctx, ids)
	if err != nil {
		return entity.PresetAgent{}, "", err
	}
	for _, role := range roles {
		if role.RoleType == entity.PresetAgentRoleTypeTeacher {
			for _, link := range links {
				if link.AgentID == role.ID {
					return role, link.VoiceID, nil
				}
			}
		}
	}
	return entity.PresetAgent{}, "", fmt.Errorf("课堂没有教师角色")
}

func persistScene(ctx context.Context, deps Deps, sceneID uint64, blocks []contentBlock, narration []narrationSegment, textOnly bool) ([]*entity.SceneSegment, error) {
	content, err := json.Marshal(map[string]any{"blocks": blocks})
	if err != nil {
		return nil, err
	}
	segments := make([]*entity.SceneSegment, 0, len(narration))
	status := entity.SceneSegmentStatusPending
	if textOnly {
		status = entity.SceneSegmentStatusReady
	}
	for i, item := range narration {
		segments = append(segments, &entity.SceneSegment{SceneID: sceneID, ContentKey: item.ContentKey, SortOrder: int32(i), Text: item.Text, Status: status})
	}
	err = deps.Tx.Run(ctx, func(txCtx context.Context) error {
		if err := deps.Segments.DeleteByScene(txCtx, sceneID); err != nil {
			return err
		}
		if err := deps.Scenes.UpdateContent(txCtx, sceneID, content); err != nil {
			return err
		}
		return deps.Segments.CreateBatch(txCtx, segments)
	})
	return segments, err
}

func synthesizeSegments(ctx context.Context, deps Deps, classroomID uint64, voice string, segments []*entity.SceneSegment) error {
	dir := filepath.Join(deps.AudioDir, strconv.FormatUint(classroomID, 10))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, segment := range segments {
		parts, err := splitTTS(segment.Text, 600)
		if err != nil {
			return err
		}
		audios := make([][]byte, 0, len(parts))
		for _, part := range parts {
			audio, synthErr := invokeWithRetryIf(ctx, maxTTSRetry, retryableTTSError, func() ([]byte, error) {
				return deps.TTS.Synthesize(ctx, part, voice)
			})
			if synthErr != nil {
				err = synthErr
				break
			}
			audios = append(audios, audio)
		}
		if err != nil {
			message := truncateRunes(err.Error(), 500)
			_ = deps.Segments.UpdateStatus(context.WithoutCancel(ctx), segment.ID, entity.SceneSegmentStatusFailed, &message)
			return fmt.Errorf("合成讲稿 %s 失败: %w", segment.ContentKey, err)
		}
		audio, err := concatWAV(audios)
		if err != nil {
			return err
		}
		name := strconv.FormatUint(segment.ID, 10) + ".wav"
		tmp := filepath.Join(dir, name+".tmp")
		if err := os.WriteFile(tmp, audio, 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			return err
		}
		rel := filepath.ToSlash(filepath.Join(strconv.FormatUint(classroomID, 10), name))
		if err := deps.Segments.UpdateAudio(ctx, segment.ID, rel, entity.SceneSegmentStatusReady); err != nil {
			return err
		}
	}
	return nil
}

func invokeWithRetry[T any](ctx context.Context, maxRetry int, invoke func() (T, error)) (T, error) {
	return invokeWithRetryIf(ctx, maxRetry, func(error) bool { return true }, invoke)
}

func invokeWithRetryIf[T any](ctx context.Context, maxRetry int, retryable func(error) bool, invoke func() (T, error)) (T, error) {
	var zero T
	var lastErr error
	for attempt := 0; attempt <= maxRetry; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return zero, ctx.Err()
			case <-time.After(retryBackoff):
			}
		}
		result, err := invoke()
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !retryable(err) {
			break
		}
	}
	return zero, lastErr
}

func retryableModelError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "429") || strings.Contains(text, "500") || strings.Contains(text, "502") || strings.Contains(text, "503") || strings.Contains(text, "504") || strings.Contains(text, "timeout") || strings.Contains(text, "temporary") || strings.Contains(text, "connection")
}

func retryableTTSError(err error) bool { return retryableModelError(err) }

func retryableSceneError(err error) bool {
	return retryableModelError(err) || strings.Contains(err.Error(), "内容块") || strings.Contains(err.Error(), "场景")
}

func persistSceneWithRetry(ctx context.Context, deps Deps, sceneID uint64, blocks []contentBlock, narration []narrationSegment, textOnly bool) ([]*entity.SceneSegment, error) {
	return invokeWithRetryIf(ctx, 1, retryableDBError, func() ([]*entity.SceneSegment, error) {
		return persistScene(ctx, deps, sceneID, blocks, narration, textOnly)
	})
}

func retryableDBError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "connection") || strings.Contains(text, "deadlock") || strings.Contains(text, "timeout") || strings.Contains(text, "temporarily")
}

func splitTTS(text string, max int) ([]string, error) {
	if len([]rune(text)) <= max {
		return []string{text}, nil
	}
	var chunks []string
	var current []rune
	for _, sentence := range strings.FieldsFunc(text, func(r rune) bool { return r == '。' || r == '！' || r == '？' || r == '；' || r == '\n' }) {
		part := []rune(strings.TrimSpace(sentence))
		if len(part) == 0 {
			continue
		}
		if len(part) > max {
			return nil, fmt.Errorf("单句讲稿超过 TTS %d 字限制", max)
		}
		if len(current)+len(part)+1 > max {
			chunks = append(chunks, string(current))
			current = nil
		}
		current = append(current, part...)
		current = append(current, '。')
	}
	if len(current) > 0 {
		chunks = append(chunks, string(current))
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("无法按句切分讲稿")
	}
	return chunks, nil
}

func concatWAV(files [][]byte) ([]byte, error) {
	if len(files) == 1 {
		return files[0], nil
	}
	var format []byte
	var pcm bytes.Buffer
	for _, file := range files {
		if len(file) < 12 || string(file[:4]) != "RIFF" || string(file[8:12]) != "WAVE" {
			return nil, fmt.Errorf("TTS 返回的不是 WAV")
		}
		pos := 12
		var data []byte
		for pos+8 <= len(file) {
			size := int(binary.LittleEndian.Uint32(file[pos+4 : pos+8]))
			end := pos + 8 + size
			if end > len(file) {
				return nil, io.ErrUnexpectedEOF
			}
			switch string(file[pos : pos+4]) {
			case "fmt ":
				format = append([]byte(nil), file[pos+8:end]...)
			case "data":
				data = file[pos+8 : end]
			}
			pos = end + (size & 1)
		}
		if len(format) == 0 || len(data) == 0 {
			return nil, fmt.Errorf("WAV 缺少 fmt 或 data")
		}
		pcm.Write(data)
	}
	if len(format) < 16 {
		return nil, fmt.Errorf("WAV fmt 无效")
	}
	result := bytes.NewBuffer(nil)
	result.WriteString("RIFF")
	binary.Write(result, binary.LittleEndian, uint32(36+pcm.Len()))
	result.WriteString("WAVEfmt ")
	binary.Write(result, binary.LittleEndian, uint32(16))
	result.Write(format[:16])
	result.WriteString("data")
	binary.Write(result, binary.LittleEndian, uint32(pcm.Len()))
	result.Write(pcm.Bytes())
	return result.Bytes(), nil
}
