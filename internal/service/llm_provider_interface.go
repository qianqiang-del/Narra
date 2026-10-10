package service

import (
	"context"
	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
)

type LLMProviderService interface {
	List(ctx context.Context, ownerID uint64) ([]responsedto.LLMProvider, error)
	AvailableModels(ctx context.Context) ([]responsedto.AvailableLLMModel, error)
	AvailableModelsForOwner(ctx context.Context, ownerID uint64) ([]responsedto.AvailableLLMModel, error)
	Create(ctx context.Context, ownerID uint64, input requestdto.LLMProvider) (*responsedto.LLMProvider, error)
	Update(ctx context.Context, ownerID, id uint64, input requestdto.LLMProvider) (*responsedto.LLMProvider, error)
	Delete(ctx context.Context, ownerID, id uint64) error
	Test(ctx context.Context, ownerID, id uint64) (*responsedto.LLMProviderTestResult, error)
	SuggestPricing(ctx context.Context, ownerID, id uint64, modelID string) (*responsedto.LLMPriceSuggestion, error)
	SetEnabled(ctx context.Context, ownerID, id uint64, enabled bool) (*responsedto.LLMProvider, error)
}
