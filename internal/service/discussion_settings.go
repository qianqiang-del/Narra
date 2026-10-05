package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"gorm.io/gorm"
	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
	apperrors "narra/pkg/errors"
)

func (s *discussionService) readModelSelection(ctx context.Context, classroomID uint64) (modelConfig, string, error) {
	classroom, err := s.classrooms.FindByID(ctx, classroomID)
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && classroom == nil) {
		return modelConfig{}, "", apperrors.New(apperrors.CodeNotFound, "课程不存在")
	}
	if err != nil {
		return modelConfig{}, "", apperrors.NewWithErr(apperrors.CodeInternalError, "查询课程失败", err)
	}
	source := "generation"
	raw := classroom.GenerationConfig
	if len(classroom.DiscussionConfig) > 0 {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(classroom.DiscussionConfig, &fields); err != nil {
			return modelConfig{}, "", apperrors.NewWithErr(apperrors.CodeInternalError, "解析讨论模型配置失败", err)
		}
		if len(fields) > 0 {
			source, raw = "classroom", classroom.DiscussionConfig
		}
	}
	var config modelConfig
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return modelConfig{}, "", apperrors.NewWithErr(apperrors.CodeInternalError, "解析课程模型配置失败", err)
	}
	return config, source, nil
}

func (s *discussionService) describeSelection(ctx context.Context, config modelConfig, source string) (*responsedto.DiscussionSettings, error) {
	if s.modelCatalog == nil {
		return nil, apperrors.New(apperrors.CodeInternalError, "讨论模型目录未配置")
	}
	rows, err := s.modelCatalog.AvailableModels(ctx)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "读取可用模型失败", err)
	}
	result := &responsedto.DiscussionSettings{ProviderID: config.ProviderID, ModelID: config.ModelID, Source: source}
	for _, row := range rows {
		if row.ProviderID == config.ProviderID && row.ModelID == config.ModelID {
			result.ProviderName, result.Available = row.ProviderName, true
			break
		}
	}
	return result, nil
}

func (s *discussionService) GetSettings(ctx context.Context, classroomID uint64) (*responsedto.DiscussionSettings, error) {
	config, source, err := s.readModelSelection(ctx, classroomID)
	if err != nil {
		return nil, err
	}
	return s.describeSelection(ctx, config, source)
}

func (s *discussionService) UpdateSettings(ctx context.Context, classroomID uint64, input requestdto.DiscussionSettings) (*responsedto.DiscussionSettings, error) {
	if input.ProviderID == 0 || strings.TrimSpace(input.ModelID) == "" {
		return nil, apperrors.New(apperrors.CodeBadRequest, "请选择服务商和模型")
	}
	// Read only for existence: a damaged previous selection must still be repairable.
	classroom, err := s.classrooms.FindByID(ctx, classroomID)
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && classroom == nil) {
		return nil, apperrors.New(apperrors.CodeNotFound, "课程不存在")
	}
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询课程失败", err)
	}
	config := modelConfig{ProviderID: input.ProviderID, ModelID: input.ModelID}
	result, err := s.describeSelection(ctx, config, "classroom")
	if err != nil {
		return nil, err
	}
	if !result.Available {
		return nil, apperrors.New(apperrors.CodeBadRequest, "所选讨论模型不可用，请先启用并测试模型配置")
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	if err := s.classrooms.UpdateDiscussionConfig(ctx, classroomID, raw); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.New(apperrors.CodeNotFound, "课程不存在")
		}
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "保存讨论模型失败", err)
	}
	return result, nil
}
