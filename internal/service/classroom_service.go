package service

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"strings"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
	"narra/internal/repository"
	apperrors "narra/pkg/errors"
)

// modelLister 提供可用模型清单，受理时校验所选模型是否可用。
type modelLister interface {
	AvailableModels(ctx context.Context) ([]responsedto.AvailableLLMModel, error)
}

// JobQueue 受理成功后投递生成任务。
type JobQueue interface {
	Enqueue(classroomID uint64) error
}

// 角色选择模式，取值与请求体的 agent_mode 一致。
const (
	agentModePreset = "preset"
	agentModeAuto   = "auto"
)

// autoStudentCount 是自动模式下抽取的学生人数。
const autoStudentCount = 3

// classroomService 课堂受理与状态查询。
type classroomService struct {
	classrooms repository.ClassroomRepository
	agents     repository.ClassroomAgentRepository
	roles      repository.RoleRepository
	models     modelLister
	queue      JobQueue
	tx         repository.TransactionManager
}

// NewClassroomService 构造课堂服务。queue 受理成功后投递生成任务。
func NewClassroomService(
	classrooms repository.ClassroomRepository,
	agents repository.ClassroomAgentRepository,
	roles repository.RoleRepository,
	models modelLister,
	queue JobQueue,
	tx repository.TransactionManager,
) ClassroomService {
	return &classroomService{
		classrooms: classrooms,
		agents:     agents,
		roles:      roles,
		models:     models,
		queue:      queue,
		tx:         tx,
	}
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

	generationConfig, err := buildGenerationConfig(input)
	if err != nil {
		return nil, err
	}
	agentConfig, err := buildAgentConfig(input)
	if err != nil {
		return nil, err
	}

	classroom := &entity.Classroom{
		Title:            truncateText(requirement, 200),
		Requirement:      requirement,
		Mode:             mode,
		Status:           entity.ClassroomStatusGenerating,
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
	LLMProviderID uint64          `json:"llm_provider_id"`
	LLMModelID    string          `json:"llm_model_id"`
	WebSearch     bool            `json:"web_search"`
	Bio           string          `json:"bio"`
	Materials     json.RawMessage `json:"materials,omitempty"`
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
		CreatedAt:       c.CreatedAt,
		UpdatedAt:       c.UpdatedAt,
	}
}
