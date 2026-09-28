package classroom

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"narra/internal/model/entity"
	"narra/pkg/logger"
)

// generateScenes 段二入口：把还没生成完的页交给页面执行器，全部结束后汇总课堂状态。
func generateScenes(ctx context.Context, deps Deps, classroom *entity.Classroom, config GenerationConfig) error {
	scenes, err := deps.Scenes.ListByClassroom(ctx, classroom.ID)
	if err != nil {
		return err
	}
	plan := classroomPlanOf(classroom, scenes)
	tasks := pendingPages(plan, scenes)
	if len(tasks) == 0 {
		return deps.Classrooms.UpdateStatus(ctx, classroom.ID, entity.ClassroomStatusReady, nil)
	}

	// 逐页抢租约需要两个标识：runID 说「这一页属于哪一轮生成」，owner 说「现在是谁在做」。
	runID, err := EnsureRunID(ctx, deps, classroom)
	if err != nil {
		return err
	}
	owner, err := newLeaseOwner(runID)
	if err != nil {
		return err
	}

	teacher, voice, err := classroomTeacher(ctx, deps, classroom.ID)
	if err != nil {
		return err
	}
	// 页面级的调研要用到与规划同一个工具集，所以联网开关要一路带到这里。
	rt, err := newRuntime(ctx, deps, config.ProviderID, config.ModelID, config.WebSearch)
	if err != nil {
		return err
	}

	executor := &pageExecutor{
		deps:      deps,
		classroom: classroom,
		teacher:   teacher,
		voice:     voice,
		rt:        rt,
		context:   buildClassroomContext(classroom, plan),
		outline:   outlineIndex(plan.Pages),
		ttsPool:   newTTSLimiter(effectiveTTSPoolSize(deps)),
		runID:     runID,
		owner:     owner,
	}
	concurrency := effectivePageConcurrency(deps)
	logger.Info("课堂页面开始生成",
		zap.Uint64("classroom_id", classroom.ID),
		zap.Int("pages", len(tasks)),
		zap.Int("concurrency", concurrency),
		zap.String("run_id", runID),
	)

	outcomes := runPages(ctx, tasks, concurrency, executor.run)
	failures := make(map[int32]string, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.err == nil {
			continue
		}
		failures[outcome.task.Scene.SortOrder] = outcome.task.Scene.Title + "：" + truncateRunes(outcome.err.Error(), 500)
	}

	// 收尾写库脱离任务 ctx：整课预算到点时这个 ctx 已经失效，而课堂状态必须落地，
	// 否则这门课会永远停在 generating，只能等对账把失败原因写成一句与事实无关的话。
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	if errors.Is(context.Cause(ctx), errBudgetExceeded) {
		// 时长预算到点：已经做完的页照常可用，没做的页留给下一次（它们的 status 还是 pending/failed）。
		// 这里必须返回 nil 而不是错误——任务层重试只会把同样长的时间再烧一遍，
		// 「部分可用 + 说明」是个明确终态，比反复重试更实在。
		if len(failures) == 0 {
			return deps.Classrooms.UpdateStatus(writeCtx, classroom.ID, entity.ClassroomStatusReady, nil)
		}
		done := len(tasks) - len(failures)
		logger.Warn("整课生成超过时长上限",
			zap.Uint64("classroom_id", classroom.ID),
			zap.String("max_duration", deps.MaxDuration.String()),
			zap.Int("done", done),
			zap.Int("pages", len(tasks)),
		)
		message := truncateRunes(fmt.Sprintf("整课生成超过时长上限（%s）：已完成 %d/%d 页，其余页可稍后重试",
			deps.MaxDuration, done, len(tasks)), 500)
		return deps.Classrooms.UpdateStatus(writeCtx, classroom.ID, entity.ClassroomStatusPlayable, &message)
	}

	if len(failures) == 0 {
		return deps.Classrooms.UpdateStatus(writeCtx, classroom.ID, entity.ClassroomStatusReady, nil)
	}

	reasons := make([]string, 0, len(failures))
	for _, task := range tasks {
		if reason, ok := failures[task.Scene.SortOrder]; ok {
			reasons = append(reasons, reason)
		}
	}
	message := truncateRunes("部分场景生成失败："+strings.Join(reasons, "；"), 500)
	return deps.Classrooms.UpdateStatus(writeCtx, classroom.ID, entity.ClassroomStatusPlayable, &message)
}

// pageTask 是段二的一页：计划里的那一页，与它对应的场景行。
type pageTask struct {
	Page  PlanPage
	Scene entity.Scene
}

// classroomPlanOf 取本课堂的计划快照；旧课堂没有快照时按场景行退化重建。
func classroomPlanOf(classroom *entity.Classroom, scenes []entity.Scene) *ClassroomPlan {
	if raw := strings.TrimSpace(string(classroom.Plan)); raw != "" && raw != "{}" {
		var plan ClassroomPlan
		if err := json.Unmarshal([]byte(raw), &plan); err == nil && len(plan.Pages) > 0 {
			return &plan
		}
	}
	return fallbackPlan(scenes)
}

// fallbackPlan 按场景行重建一份计划，供改造之前生成、没有计划快照的课堂继续生成。
func fallbackPlan(scenes []entity.Scene) *ClassroomPlan {
	plan := &ClassroomPlan{Version: initialPlanVersion}
	for _, scene := range scenes {
		if scene.Type == entity.SceneTypeComplete {
			continue
		}
		plan.Pages = append(plan.Pages, PlanPage{
			PlanID:  fmt.Sprintf("page-%02d", scene.SortOrder+1),
			Order:   int(scene.SortOrder),
			Type:    scene.Type,
			Title:   scene.Title,
			Brief:   scene.Brief,
			SceneID: scene.ID,
		})
	}
	return plan
}

// pendingPages 挑出还没生成完的页，并把计划里的页与场景行配对。
func pendingPages(plan *ClassroomPlan, scenes []entity.Scene) []pageTask {
	byOrder := make(map[int]entity.Scene, len(scenes))
	for _, scene := range scenes {
		byOrder[int(scene.SortOrder)] = scene
	}
	tasks := make([]pageTask, 0, len(plan.Pages))
	for _, page := range plan.Pages {
		scene, ok := byOrder[page.Order]
		if !ok || scene.Type == entity.SceneTypeComplete || scene.Status == entity.SceneStatusReady {
			continue
		}
		tasks = append(tasks, pageTask{Page: page, Scene: scene})
	}
	return tasks
}

// classroomTeacher 取本课堂的教师角色与音色。
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

// persistScene 在一个事务里写页面内容、审核摘要、交互 HTML 与讲解段落，并返回落库后的段落。
//
// 段落是按 content_key 幂等替换的：讲稿没变的段落仍是库里原来那行、音频路径照旧。
// 所以返回的必须是读回来的那一份，而不是内存里的草稿——接着合成语音要靠库里的主键与状态，
// 草稿里的主键是空的。
//
// html 只有交互页非空；写空串会覆盖掉上一版的 HTML，这是对的：内容重做过就该跟着换。
func persistScene(ctx context.Context, deps Deps, sceneID uint64, owner string, blocks []contentBlock, narration []narrationSegment, review json.RawMessage, html string, textOnly bool) ([]entity.SceneSegment, error) {
	content, err := json.Marshal(map[string]any{"blocks": blocks})
	if err != nil {
		return nil, err
	}
	status := entity.SceneSegmentStatusPending
	if textOnly {
		status = entity.SceneSegmentStatusReady
	}
	segments := make([]*entity.SceneSegment, 0, len(narration))
	for i, item := range narration {
		segments = append(segments, &entity.SceneSegment{SceneID: sceneID, ContentKey: item.ContentKey, SortOrder: int32(i), Text: item.Text, Status: status})
	}

	var stored []entity.SceneSegment
	err = deps.Tx.Run(ctx, func(txCtx context.Context) error {
		// 先写内容：这一步带租约校验，租约易主时整个事务回滚，段落替换也一并撤掉，
		// 不会留下「内容没换、音频却按新讲稿洗过一遍」的半截状态。
		if err := deps.Scenes.UpdateContent(txCtx, sceneID, owner, content, review, html); err != nil {
			return err
		}
		if err := deps.Segments.ReplaceByScene(txCtx, sceneID, segments); err != nil {
			return err
		}
		var listErr error
		stored, listErr = deps.Segments.ListByScene(txCtx, sceneID)
		return listErr
	})
	if err != nil {
		return nil, err
	}
	return stored, nil
}

// persistSceneWithRetry 落库失败按可重试的数据库错误重试一次。
func persistSceneWithRetry(ctx context.Context, deps Deps, sceneID uint64, owner string, blocks []contentBlock, narration []narrationSegment, review json.RawMessage, html string, textOnly bool) ([]entity.SceneSegment, error) {
	return invokeWithRetryIf(ctx, 1, retryableDBError, func() ([]entity.SceneSegment, error) {
		return persistScene(ctx, deps, sceneID, owner, blocks, narration, review, html, textOnly)
	})
}

// synthesizeSegments 逐段合成语音，跳过已经合成好的段落。
//
// 跳过是为了断点续传：重投时内容与讲稿都在库里，只差几段音频，只该补那几段，
// 而不是把整页重讲一遍。已经合成好的段落在 ReplaceByScene 里就保住了音频路径，
// 这里再认一次状态，两条路都能少做无用功。
//
// 每段音频写库都要带 owner：段落本身没有租约，靠所属页面的租约保护。页面租约易主后，
// 迟到的音频会被挡在库外——否则它会把上一版讲稿的录音挂到已经改过的段落上。
func synthesizeSegments(ctx context.Context, deps Deps, classroomID, sceneID uint64, owner, voice string, segments []entity.SceneSegment, pool ttsLimiter) error {
	dir := filepath.Join(deps.AudioDir, strconv.FormatUint(classroomID, 10))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for i := range segments {
		segment := &segments[i]
		if segmentAudioReady(segment) {
			continue
		}
		parts, err := splitTTS(segment.Text, 600)
		if err != nil {
			return err
		}
		audios := make([][]byte, 0, len(parts))
		for _, part := range parts {
			audio, synthErr := invokeWithRetryIf(ctx, maxTTSRetry, retryableTTSError, func() ([]byte, error) {
				if err := pool.acquire(ctx); err != nil {
					return nil, err
				}
				defer pool.release()
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
			_ = deps.Segments.UpdateStatus(context.WithoutCancel(ctx), sceneID, segment.ID, owner, entity.SceneSegmentStatusFailed, &message)
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
		if err := deps.Segments.UpdateAudio(ctx, sceneID, segment.ID, owner, rel, entity.SceneSegmentStatusReady); err != nil {
			return err
		}
	}
	return nil
}

// segmentAudioReady 报告这一段的音频是否已经落库：状态 ready 且音频路径非空。
func segmentAudioReady(segment *entity.SceneSegment) bool {
	return segment.Status == entity.SceneSegmentStatusReady &&
		segment.AudioPath != nil && strings.TrimSpace(*segment.AudioPath) != ""
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
