package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
	"narra/internal/repository"
	apperrors "narra/pkg/errors"
	"narra/pkg/logger"
	"narra/pkg/utils"
)

// modelLister 提供可用模型清单，受理时校验所选模型是否可用。
type modelLister interface {
	AvailableModels(ctx context.Context) ([]responsedto.AvailableLLMModel, error)
}

// materialDocumentReader 是课程材料校验与生命周期对知识库的最小依赖面。
type materialDocumentReader interface {
	GetByID(ctx context.Context, id uint64) (*entity.KnowledgeDocument, error)
	// AssociateMaterials 把待用材料关联到课堂（expires_at 置空）；返回实际关联行数，
	// 调用方核对是否等于材料数，不等就回滚（材料已被别的课堂用掉或已失效）。
	AssociateMaterials(ctx context.Context, ids []uint64) (int64, error)
	// ExpireMaterials 给材料重设清理时间（删课堂时回收）。
	ExpireMaterials(ctx context.Context, ids []uint64, expiresAt time.Time) (int64, error)
}

// JobQueue 受理成功后投递生成任务，删除课堂时撤掉它。
type JobQueue interface {
	Enqueue(classroomID uint64) error
	Remove(classroomID uint64) error
}

type SceneRetryQueue interface {
	EnqueueScene(classroomID, sceneID uint64) error
}

// 角色选择模式，取值与请求体的 agent_mode 一致。
const (
	agentModePreset = "preset"
	agentModeAuto   = "auto"
)

// autoStudentCount 是自动模式下抽取的学生人数。
const autoStudentCount = 3

// maxClassroomMaterials 是一次生成最多携带的课程材料数，与知识库批量上传的文件数上限一致。
const maxClassroomMaterials = 10

// classroomService 课堂受理与状态查询。
type classroomService struct {
	classrooms repository.ClassroomRepository
	agents     repository.ClassroomAgentRepository
	roles      repository.RoleRepository
	scenes     repository.SceneRepository
	models     modelLister
	materials  materialDocumentReader
	queue      JobQueue
	tx         repository.TransactionManager
	// audio 是课堂音频的清理入口（本地目录或对象存储）；nil 表示未接入，删除时跳过。
	audio classroomAudioStore
}

// classroomAudioStore 是删除课堂时对音频存储的最小依赖面；
// classroom.AudioStore（本地实现与 OSS 实现）都满足它。
type classroomAudioStore interface {
	RemoveClassroom(classroomID uint64) error
}

// NewClassroomService 构造课堂服务。queue 受理成功后投递生成任务，audio 供删除课堂时清理音频。
func NewClassroomService(
	classrooms repository.ClassroomRepository,
	agents repository.ClassroomAgentRepository,
	roles repository.RoleRepository,
	scenes repository.SceneRepository,
	models modelLister,
	materials materialDocumentReader,
	queue JobQueue,
	tx repository.TransactionManager,
	audio classroomAudioStore,
) ClassroomService {
	return &classroomService{
		classrooms: classrooms,
		agents:     agents,
		roles:      roles,
		scenes:     scenes,
		models:     models,
		materials:  materials,
		queue:      queue,
		tx:         tx,
		audio:      audio,
	}
}

func (s *classroomService) GetOutline(ctx context.Context, id uint64) (*responsedto.ClassroomOutline, error) {
	classroom, err := s.classrooms.FindByID(ctx, id)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeNotFound, "课堂不存在", err)
	}
	scenes, err := s.scenes.ListByClassroom(ctx, id)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询课堂大纲失败", err)
	}
	items := make([]responsedto.OutlineScene, 0, len(scenes))
	for _, scene := range scenes {
		if scene.Type == entity.SceneTypeComplete {
			continue
		}
		items = append(items, responsedto.OutlineScene{ID: scene.ID, SortOrder: scene.SortOrder, Type: scene.Type, Title: scene.Title, Brief: scene.Brief, Status: scene.Status})
	}
	return &responsedto.ClassroomOutline{ClassroomID: id, Title: classroom.Title, Scenes: items}, nil
}

func (s *classroomService) GetAgents(ctx context.Context, id uint64) ([]responsedto.RoleItem, error) {
	if _, err := s.classrooms.FindByID(ctx, id); err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeNotFound, "课堂不存在", err)
	}
	snapshots, err := s.agents.ListByClassroom(ctx, id)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询课堂角色失败", err)
	}
	if len(snapshots) == 0 {
		return []responsedto.RoleItem{}, nil
	}
	ids := make([]uint64, 0, len(snapshots))
	voiceByID := make(map[uint64]string, len(snapshots))
	for _, snapshot := range snapshots {
		ids = append(ids, snapshot.AgentID)
		voiceByID[snapshot.AgentID] = snapshot.VoiceID
	}
	roles, err := s.roles.ListByIDs(ctx, ids)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询课堂角色失败", err)
	}
	items := make([]responsedto.RoleItem, 0, len(roles))
	for _, role := range roles {
		items = append(items, responsedto.RoleItem{
			AgentKey: role.AgentKey, Name: role.Name, Role: role.Role,
			RoleType: role.RoleType, Persona: role.Persona, Avatar: role.Avatar,
			Color: role.Color, VoiceID: voiceByID[role.ID], SortOrder: role.SortOrder,
		})
	}
	return items, nil
}

func (s *classroomService) ListScenes(ctx context.Context, id uint64) ([]responsedto.ClassroomSceneSummary, error) {
	if _, err := s.classrooms.FindByID(ctx, id); err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeNotFound, "课堂不存在", err)
	}
	scenes, err := s.scenes.ListByClassroom(ctx, id)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询课堂场景失败", err)
	}
	items := make([]responsedto.ClassroomSceneSummary, 0, len(scenes))
	for _, scene := range scenes {
		if scene.Type == entity.SceneTypeComplete {
			continue
		}
		items = append(items, responsedto.ClassroomSceneSummary{
			ID: scene.ID, SortOrder: scene.SortOrder, Type: scene.Type,
			Title: scene.Title, Status: scene.Status, Phase: scene.Phase, ErrorMessage: scene.ErrorMessage,
		})
	}
	return items, nil
}

func (s *classroomService) RetryScene(ctx context.Context, sceneID uint64) (*responsedto.ClassroomSceneSummary, error) {
	scene, err := s.scenes.FindByID(ctx, sceneID)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeNotFound, "场景不存在", err)
	}
	if scene.Status != entity.SceneStatusFailed {
		return nil, apperrors.New(apperrors.CodeConflict, "这一页当前不是失败状态，不能重试")
	}
	queue, ok := s.queue.(SceneRetryQueue)
	if !ok {
		return nil, apperrors.New(apperrors.CodeInternalError, "页面重试队列未配置")
	}
	classroom, err := s.classrooms.FindByID(ctx, scene.ClassroomID)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeNotFound, "课堂不存在", err)
	}
	reset, err := s.scenes.ResetForRetry(ctx, sceneID)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "准备页面重试失败", err)
	}
	if !reset {
		return nil, apperrors.New(apperrors.CodeConflict, "这一页已经被重试或正在生成")
	}
	if err := s.classrooms.UpdateStatus(ctx, classroom.ID, entity.ClassroomStatusPlayable, nil); err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "更新课堂状态失败", err)
	}
	if err := queue.EnqueueScene(classroom.ID, sceneID); err != nil {
		_ = s.scenes.RestoreRetryFailure(ctx, sceneID, "页面重试任务投递失败，请稍后重试")
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "页面重试任务投递失败", err)
	}
	return &responsedto.ClassroomSceneSummary{
		ID: scene.ID, SortOrder: scene.SortOrder, Type: scene.Type, Title: scene.Title,
		Status: entity.SceneStatusPending, Phase: "", ErrorMessage: nil,
	}, nil
}

// Create 校验入参、落一行 generating、投递队列，立刻返回。
func (s *classroomService) Create(ctx context.Context, input requestdto.CreateClassroom) (*responsedto.Classroom, error) {
	requirement := strings.TrimSpace(input.Requirement)
	if requirement == "" {
		return nil, apperrors.New(apperrors.CodeBadRequest, "生成需求不能为空")
	}
	mode := input.Mode
	if mode == "" {
		mode = entity.ClassroomModeVocational
	}
	if mode != entity.ClassroomModeVocational && mode != entity.ClassroomModeInteractive {
		return nil, apperrors.New(apperrors.CodeBadRequest, "课程模式无效")
	}
	if err := s.validateModel(ctx, input.LLMProviderID, input.LLMModelID); err != nil {
		return nil, err
	}
	// 角色与音色先解析：这一步会校验勾选的角色和音色，失败时还没落库，不必回滚。
	agents, err := s.resolveAgents(ctx, input)
	if err != nil {
		return nil, err
	}
	// 课程材料只收"现在就检索得到"的文档；名字/大小的归一化结果随后写进生成配置。
	materials, err := s.normalizeMaterials(ctx, input.Materials)
	if err != nil {
		return nil, err
	}
	input.Materials = materials

	generationConfig, err := buildGenerationConfig(input)
	if err != nil {
		return nil, err
	}
	agentConfig, err := buildAgentConfig(input)
	if err != nil {
		return nil, err
	}

	runID, err := utils.RandomHex(16)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "生成运行标识失败", err)
	}
	classroom := &entity.Classroom{
		Title:            truncateText(requirement, 200),
		Requirement:      requirement,
		Mode:             mode,
		Status:           entity.ClassroomStatusGenerating,
		GenerationRunID:  runID,
		GenerationConfig: generationConfig,
		AgentConfig:      agentConfig,
	}
	if s.tx == nil {
		return nil, apperrors.New(apperrors.CodeInternalError, "创建课堂事务未配置")
	}
	if err := s.tx.Run(ctx, func(txCtx context.Context) error {
		if err := s.classrooms.Create(txCtx, classroom); err != nil {
			return apperrors.NewWithErr(apperrors.CodeInternalError, "创建课堂失败", err)
		}
		for index := range agents {
			agents[index].ClassroomID = classroom.ID
		}
		if err := s.agents.CreateBatch(txCtx, agents); err != nil {
			return apperrors.NewWithErr(apperrors.CodeInternalError, "写入课堂角色失败", err)
		}
		// 材料"转正"与建课同事务：把待用材料的 expires_at 置空。
		// 行数对不上说明材料已被别的课堂用掉或已失效（并发/过期/删除），整体回滚。
		if ids := materialDocumentIDs(input.Materials); len(ids) > 0 {
			applied, err := s.materials.AssociateMaterials(txCtx, ids)
			if err != nil {
				return apperrors.NewWithErr(apperrors.CodeInternalError, "关联课程材料失败", err)
			}
			if applied != int64(len(ids)) {
				return apperrors.New(apperrors.CodeBadRequest, "课程材料已被其他课堂使用或已失效，请重新选择")
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := s.queue.Enqueue(classroom.ID); err != nil {
		// 课已落库、任务却没投出去，置成 failed，免得它永远停在 generating。
		reason := "投递生成任务失败，请重新发起"
		if markErr := s.classrooms.UpdateStatus(ctx, classroom.ID, entity.ClassroomStatusFailed, &reason); markErr != nil {
			return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "投递生成任务失败且无法标记状态", markErr)
		}
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "投递生成任务失败", err)
	}
	return toClassroomResponse(*classroom), nil
}

// resolveAgents 组装课堂角色快照：教师固定取池里第一个，学员按角色选择模式决定。
func (s *classroomService) resolveAgents(ctx context.Context, input requestdto.CreateClassroom) ([]entity.ClassroomAgent, error) {
	pool, err := s.roles.ListEnabled(ctx)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询角色池失败", err)
	}

	byKey := make(map[string]entity.PresetAgent, len(pool))
	var teacher *entity.PresetAgent
	var assistant *entity.PresetAgent
	students := make([]entity.PresetAgent, 0, len(pool))
	for index := range pool {
		agent := &pool[index]
		byKey[agent.AgentKey] = *agent
		switch agent.RoleType {
		case entity.PresetAgentRoleTypeTeacher:
			if teacher == nil {
				teacher = agent
			}
		case entity.PresetAgentRoleTypeAssistant:
			if assistant == nil {
				assistant = agent
			}
		default:
			students = append(students, *agent)
		}
	}
	if teacher == nil {
		return nil, apperrors.New(apperrors.CodeInternalError, "角色池里没有可用的教师")
	}

	teacherVoice, err := pickVoice(input.TeacherVoice, teacher.VoiceID)
	if err != nil {
		return nil, err
	}

	agents := make([]entity.ClassroomAgent, 0, len(input.RoleIDs)+2)
	seen := make(map[uint64]struct{}, len(input.RoleIDs)+2)
	seen[teacher.ID] = struct{}{}
	agents = append(agents, entity.ClassroomAgent{AgentID: teacher.ID, VoiceID: teacherVoice})

	if input.AgentMode == agentModeAuto {
		if assistant != nil {
			seen[assistant.ID] = struct{}{}
			agents = append(agents, entity.ClassroomAgent{AgentID: assistant.ID, VoiceID: assistant.VoiceID})
		}
		for _, agent := range pickRandom(students, autoStudentCount) {
			if _, exists := seen[agent.ID]; exists {
				continue
			}
			seen[agent.ID] = struct{}{}
			agents = append(agents, entity.ClassroomAgent{AgentID: agent.ID, VoiceID: agent.VoiceID})
		}
		return agents, nil
	}

	for _, key := range input.RoleIDs {
		agent, ok := byKey[strings.TrimSpace(key)]
		if !ok {
			return nil, apperrors.New(apperrors.CodeBadRequest, "所选角色不可用，请重新选择")
		}
		if _, exists := seen[agent.ID]; exists {
			continue
		}
		voice, err := pickVoice(input.RoleVoices[agent.AgentKey], agent.VoiceID)
		if err != nil {
			return nil, err
		}
		seen[agent.ID] = struct{}{}
		agents = append(agents, entity.ClassroomAgent{AgentID: agent.ID, VoiceID: voice})
	}
	return agents, nil
}

// pickRandom 从角色里随机取 count 个；池子不足时全取。
func pickRandom(pool []entity.PresetAgent, count int) []entity.PresetAgent {
	if count >= len(pool) {
		return pool
	}
	picked := append([]entity.PresetAgent(nil), pool...)
	for index := len(picked) - 1; index > 0; index-- {
		swap := rand.IntN(index + 1)
		picked[index], picked[swap] = picked[swap], picked[index]
	}
	return picked[:count]
}

// pickVoice 取用户选定的音色；没选就用角色默认音色，选了但不在目录里则报错。
func pickVoice(chosen string, fallback string) (string, error) {
	voice := strings.TrimSpace(chosen)
	if voice == "" {
		return fallback, nil
	}
	if !IsValidVoiceID(voice) {
		return "", apperrors.New(apperrors.CodeBadRequest, "所选音色不可用，请重新选择")
	}
	return voice, nil
}

// Get 查一门课的状态，供前端轮询。
func (s *classroomService) Get(ctx context.Context, id uint64) (*responsedto.Classroom, error) {
	classroom, err := s.classrooms.FindByID(ctx, id)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeNotFound, "课堂不存在", err)
	}
	detail := toClassroomResponse(*classroom)
	agents, err := s.listAgentBriefs(ctx, id)
	if err != nil {
		return nil, err
	}
	detail.Agents = agents
	return detail, nil
}

// List 查课堂列表，连卡片要用的页数、已就绪页数与封面首页一并带上。
//
// 统计与封面各自一次批量查询解决，不按课程逐门去查场景：卡片按门数摊开，逐门查就是 N+1。
func (s *classroomService) List(ctx context.Context) ([]*responsedto.ClassroomListItem, error) {
	classrooms, err := s.classrooms.List(ctx)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询课堂列表失败", err)
	}
	ids := make([]uint64, 0, len(classrooms))
	for _, classroom := range classrooms {
		ids = append(ids, classroom.ID)
	}
	stats, err := s.scenes.CountSceneStatsByClassrooms(ctx, ids)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询课堂页数失败", err)
	}
	covers, err := s.scenes.ListFirstByClassrooms(ctx, ids)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询课堂封面失败", err)
	}
	items := make([]*responsedto.ClassroomListItem, 0, len(classrooms))
	for _, classroom := range classrooms {
		stat := stats[classroom.ID]
		item := &responsedto.ClassroomListItem{Classroom: *toClassroomResponse(classroom)}
		item.Agents = []responsedto.ClassroomAgentBrief{}
		item.Pages = int(stat.Pages)
		item.ReadyPages = int(stat.ReadyPages)
		if scene, ok := covers[classroom.ID]; ok {
			item.Cover = &responsedto.ClassroomCoverScene{
				ID: scene.ID, Type: scene.Type, Title: scene.Title, Status: scene.Status,
				Content: scene.Content, InteractiveHTML: scene.InteractiveHTML,
			}
		}
		items = append(items, item)
	}
	return items, nil
}

// Delete 删除课堂：先撤掉生成任务，再删库，最后清音频。
//
// 顺序有讲究。先撤任务：否则删完库还留着页面继续跑模型，白花钱。
// 最后清音频：库删干净之后 data/audio/<id>/ 里那些 wav 就再没人引用了，留着只是垃圾。
//
// 「停止写旧结果」不在这里做，也不该在这里做：页面每次写库都带页面租约校验，
// 行都被级联删掉了，旧执行者的写入自然影响 0 行——靠代码结构保证，不靠删除方记得去拦。
func (s *classroomService) Delete(ctx context.Context, id uint64) error {
	classroom, err := s.classrooms.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperrors.NewWithErr(apperrors.CodeNotFound, "课堂不存在", err)
		}
		return apperrors.NewWithErr(apperrors.CodeInternalError, "查询课堂失败", err)
	}
	if err := s.queue.Remove(id); err != nil {
		// 撤任务失败不拦着删课：任务真跑起来发现课没了会自己结束（Generate 读不到课程直接返回），
		// 用户想删的课却删不掉才是更糟的结果。
		logger.Warn("撤销生成任务失败，继续删除课堂", zap.Uint64("classroom_id", id), zap.Error(err))
	}
	if s.tx == nil {
		return apperrors.New(apperrors.CodeInternalError, "删除课堂事务未配置")
	}

	// 删课回收：材料随课走，回到"待清理"状态，由 retention 下一轮删除。
	// 必须在删掉课行之后再设 expires_at（同一事务内），否则解析出的名单会把本课自己算进去；
	// 材料不存在（已被手动删除）时 ExpireMaterials 命中 0 行，不是错误。
	recycled := classroomMaterialIDs(classroom.GenerationConfig)
	err = s.tx.Run(ctx, func(txCtx context.Context) error {
		if err := s.classrooms.Delete(txCtx, id); err != nil {
			return err
		}
		if len(recycled) > 0 {
			if _, err := s.materials.ExpireMaterials(txCtx, recycled, time.Now().UTC()); err != nil {
				return fmt.Errorf("回收课程材料失败: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperrors.NewWithErr(apperrors.CodeNotFound, "课堂不存在", err)
		}
		return apperrors.NewWithErr(apperrors.CodeInternalError, "删除课堂失败", err)
	}
	if err := s.removeAudio(id); err != nil {
		logger.Warn("清理课堂音频失败", zap.Uint64("classroom_id", id), zap.Error(err))
	}
	return nil
}

// materialDocumentIDs 取材料引用的文档 ID；入参在受理阶段已经去重。
func materialDocumentIDs(materials []requestdto.CreateClassroomMaterial) []uint64 {
	if len(materials) == 0 {
		return nil
	}
	ids := make([]uint64, 0, len(materials))
	for _, material := range materials {
		if material.DocumentID != 0 {
			ids = append(ids, material.DocumentID)
		}
	}
	return ids
}

// classroomMaterialIDs 从课堂的生成配置快照里取材料文档 ID；旧课堂没有这个键时返回空。
func classroomMaterialIDs(raw json.RawMessage) []uint64 {
	if len(raw) == 0 {
		return nil
	}
	var config struct {
		Materials []requestdto.CreateClassroomMaterial `json:"materials"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil
	}
	return materialDocumentIDs(config.Materials)
}

// removeAudio 删掉这门课的音频（本地目录或对象存储）。
//
// 本地模式下删的是本程序自己写出去的目录，路径由音频根目录加课堂 ID 拼成；
// 对象存储模式下按知识空间里的音频前缀删除，社区 ID 同样来自数据库，不含外部输入。
func (s *classroomService) removeAudio(classroomID uint64) error {
	if s.audio == nil {
		return nil
	}
	if err := s.audio.RemoveClassroom(classroomID); err != nil {
		return fmt.Errorf("删除课堂 %d 的音频失败: %w", classroomID, err)
	}
	return nil
}

// listAgentBriefs 取课堂角色的最小信息：音色来自快照表，agent_key 由角色池换回。
func (s *classroomService) listAgentBriefs(ctx context.Context, classroomID uint64) ([]responsedto.ClassroomAgentBrief, error) {
	snapshots, err := s.agents.ListByClassroom(ctx, classroomID)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询课堂角色失败", err)
	}
	if len(snapshots) == 0 {
		return []responsedto.ClassroomAgentBrief{}, nil
	}

	ids := make([]uint64, 0, len(snapshots))
	voiceByID := make(map[uint64]string, len(snapshots))
	for _, item := range snapshots {
		ids = append(ids, item.AgentID)
		voiceByID[item.AgentID] = item.VoiceID
	}

	presets, err := s.roles.ListByIDs(ctx, ids)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询角色失败", err)
	}

	// ListByIDs 已按 sort_order 升序，照它的顺序输出即可。
	briefs := make([]responsedto.ClassroomAgentBrief, 0, len(presets))
	for _, agent := range presets {
		voice, ok := voiceByID[agent.ID]
		if !ok {
			continue
		}
		briefs = append(briefs, responsedto.ClassroomAgentBrief{
			AgentKey: agent.AgentKey,
			VoiceID:  voice,
		})
	}
	return briefs, nil
}

// normalizeMaterials 校验并归一化课程材料引用。
//
// 只收已经能参与检索的文档：不存在 / 还没收录完 / 已停用一律拒绝，避免"课建了、
// 材料却永远检索不到"的沉默失败。名字是展示快照，缺省回落到文档标题；大小不合法按 0。
func (s *classroomService) normalizeMaterials(ctx context.Context, materials []requestdto.CreateClassroomMaterial) ([]requestdto.CreateClassroomMaterial, error) {
	if len(materials) == 0 {
		return nil, nil
	}
	if len(materials) > maxClassroomMaterials {
		return nil, apperrors.New(apperrors.CodeBadRequest, fmt.Sprintf("一次最多携带 %d 份课程材料", maxClassroomMaterials))
	}
	if s.materials == nil {
		return nil, apperrors.New(apperrors.CodeInternalError, "校验课程材料失败：缺少知识库依赖")
	}

	seen := make(map[uint64]struct{}, len(materials))
	out := make([]requestdto.CreateClassroomMaterial, 0, len(materials))
	for _, material := range materials {
		if material.DocumentID == 0 {
			return nil, apperrors.New(apperrors.CodeBadRequest, "课程材料缺少文档 ID")
		}
		if _, duplicated := seen[material.DocumentID]; duplicated {
			return nil, apperrors.New(apperrors.CodeBadRequest, "同一份课程材料重复提交")
		}
		seen[material.DocumentID] = struct{}{}

		document, err := s.materials.GetByID(ctx, material.DocumentID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, apperrors.New(apperrors.CodeBadRequest, "课程材料不存在或已被删除")
			}
			return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询课程材料失败", err)
		}
		if document.Status != entity.KnowledgeDocumentStatusReady {
			return nil, apperrors.New(apperrors.CodeBadRequest, "课程材料还没处理完成，请稍后再试")
		}
		if !document.Enabled {
			return nil, apperrors.New(apperrors.CodeBadRequest, "课程材料已被停用，请启用后再试")
		}
		// 材料不共享：只收"待使用"的课程材料（未关联课堂，带 expires_at）。
		// 已关联的材料、普通知识库文档都不允许再挂到另一门课。
		if document.Kind != entity.KnowledgeDocumentKindMaterial || document.ExpiresAt == nil {
			return nil, apperrors.New(apperrors.CodeBadRequest, "只能引用待使用的课程材料，请重新上传")
		}

		name := strings.TrimSpace(material.Name)
		if name == "" {
			name = document.Title
		}
		material.Name = truncateText(name, 300)
		if material.Size < 0 {
			material.Size = 0
		}
		out = append(out, material)
	}
	return out, nil
}

// validateModel 校验 provider + model 在可用列表里。
func (s *classroomService) validateModel(ctx context.Context, providerID uint64, modelID string) error {
	models, err := s.models.AvailableModels(ctx)
	if err != nil {
		return apperrors.NewWithErr(apperrors.CodeInternalError, "查询可用模型失败", err)
	}
	for _, m := range models {
		if m.ProviderID == providerID && m.ModelID == modelID {
			return nil
		}
	}
	return apperrors.New(apperrors.CodeBadRequest, "所选模型不可用，请先在设置里配置并测试大模型")
}

// generationConfigJSON 是落进 classrooms.generation_config 的字段。
type generationConfigJSON struct {
	LLMProviderID uint64                               `json:"llm_provider_id"`
	LLMModelID    string                               `json:"llm_model_id"`
	WebSearch     bool                                 `json:"web_search"`
	Bio           string                               `json:"bio"`
	Materials     []requestdto.CreateClassroomMaterial `json:"materials,omitempty"`
}

// agentConfigJSON 是落进 classrooms.agent_config 的字段。
type agentConfigJSON struct {
	Mode         string   `json:"mode"`
	RoleIDs      []string `json:"role_ids,omitempty"`
	TeacherVoice string   `json:"teacher_voice,omitempty"`
}

func buildGenerationConfig(input requestdto.CreateClassroom) (json.RawMessage, error) {
	raw, err := json.Marshal(generationConfigJSON{
		LLMProviderID: input.LLMProviderID,
		LLMModelID:    input.LLMModelID,
		WebSearch:     input.WebSearch,
		Bio:           strings.TrimSpace(input.Bio),
		Materials:     input.Materials,
	})
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "序列化生成配置失败", err)
	}
	return raw, nil
}

func buildAgentConfig(input requestdto.CreateClassroom) (json.RawMessage, error) {
	mode := input.AgentMode
	if mode == "" {
		mode = agentModePreset
	}
	raw, err := json.Marshal(agentConfigJSON{
		Mode:         mode,
		RoleIDs:      input.RoleIDs,
		TeacherVoice: strings.TrimSpace(input.TeacherVoice),
	})
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "序列化角色配置失败", err)
	}
	return raw, nil
}

func toClassroomResponse(c entity.Classroom) *responsedto.Classroom {
	return &responsedto.Classroom{
		ID:              c.ID,
		FolderID:        c.FolderID,
		Title:           c.Title,
		Requirement:     c.Requirement,
		Mode:            c.Mode,
		Status:          c.Status,
		GenerationError: c.GenerationError,
		Materials:       classroomMaterialsOf(c.GenerationConfig),
		CreatedAt:       c.CreatedAt,
		UpdatedAt:       c.UpdatedAt,
	}
}

// classroomMaterialsOf 从课堂的生成配置快照里取材料清单；旧课堂没有这个键时返回空数组。
func classroomMaterialsOf(raw json.RawMessage) []responsedto.ClassroomMaterial {
	materials := []responsedto.ClassroomMaterial{}
	if len(raw) == 0 {
		return materials
	}
	var config struct {
		Materials []requestdto.CreateClassroomMaterial `json:"materials"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return materials
	}
	for _, material := range config.Materials {
		materials = append(materials, responsedto.ClassroomMaterial{
			DocumentID: material.DocumentID,
			Name:       material.Name,
			Size:       material.Size,
		})
	}
	return materials
}
