package service

import (
	"context"
	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
)

type LLMProviderService interface {
	List(ctx context.Context) ([]responsedto.LLMProvider, error)
	AvailableModels(ctx context.Context) ([]responsedto.AvailableLLMModel, error)
	Create(ctx context.Context, input requestdto.LLMProvider) (*responsedto.LLMProvider, error)
	Update(ctx context.Context, id uint64, input requestdto.LLMProvider) (*responsedto.LLMProvider, error)
	Delete(ctx context.Context, id uint64) error
	Test(ctx context.Context, id uint64) (*responsedto.LLMProviderTestResult, error)
	SetEnabled(ctx context.Context, id uint64, enabled bool) (*responsedto.LLMProvider, error)
}
