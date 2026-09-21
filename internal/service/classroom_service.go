package service

import (
	"context"
	"encoding/json"
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

// classroomService 课堂受理与状态查询。
type classroomService struct {
	classrooms repository.ClassroomRepository
	models     modelLister
	queue      JobQueue
}

// NewClassroomService 构造课堂服务。queue 受理成功后投递生成任务。
func NewClassroomService(classrooms repository.ClassroomRepository, models modelLister, queue JobQueue) ClassroomService {
	return &classroomService{classrooms: classrooms, models: models, queue: queue}
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
	if err := s.classrooms.Create(ctx, classroom); err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "创建课堂失败", err)
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

// Get 查一门课的状态，供前端轮询。
func (s *classroomService) Get(ctx context.Context, id uint64) (*responsedto.Classroom, error) {
	classroom, err := s.classrooms.FindByID(ctx, id)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeNotFound, "课堂不存在", err)
	}
	return toClassroomResponse(*classroom), nil
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
		mode = "preset"
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
