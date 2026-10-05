package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"gorm.io/gorm"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
	"narra/internal/repository"
)

type discussionSettingsClassrooms struct {
	repository.ClassroomRepository
	items   map[uint64]*entity.Classroom
	saves   int
	saveErr error
}

func (r *discussionSettingsClassrooms) FindByID(_ context.Context, id uint64) (*entity.Classroom, error) {
	if item := r.items[id]; item != nil {
		return item, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *discussionSettingsClassrooms) UpdateDiscussionConfig(_ context.Context, id uint64, config json.RawMessage) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.items[id].DiscussionConfig = append(json.RawMessage(nil), config...)
	r.saves++
	return nil
}

type discussionSettingsCatalog struct {
	rows []responsedto.AvailableLLMModel
	err  error
}

func (c discussionSettingsCatalog) AvailableModels(context.Context) ([]responsedto.AvailableLLMModel, error) {
	return c.rows, c.err
}

func TestDiscussionSettingsDefaultAndIsolation(t *testing.T) {
	r := &discussionSettingsClassrooms{items: map[uint64]*entity.Classroom{
		1: {GenerationConfig: json.RawMessage(`{"llm_provider_id":1,"llm_model_id":"chat"}`)},
		2: {GenerationConfig: json.RawMessage(`{"llm_provider_id":1,"llm_model_id":"chat"}`)},
	}}
	s := &discussionService{classrooms: r, modelCatalog: discussionSettingsCatalog{rows: []responsedto.AvailableLLMModel{
		{ProviderID: 1, ProviderName: "A", ModelID: "chat"}, {ProviderID: 2, ProviderName: "B", ModelID: "chat"},
	}}}
	ctx := context.Background()
	initial, err := s.GetSettings(ctx, 1)
	if err != nil || initial.Source != "generation" || !initial.Available || initial.ProviderID != 1 {
		t.Fatalf("default: %+v %v", initial, err)
	}
	previous, err := s.modelSnapshot(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := s.UpdateSettings(ctx, 1, requestdto.DiscussionSettings{ProviderID: 2, ModelID: "chat"})
	if err != nil || changed.Source != "classroom" || changed.ProviderID != 2 {
		t.Fatalf("updated: %+v %v", changed, err)
	}
	current, err := s.modelSnapshot(ctx, 1)
	if err != nil || current.ProviderID != 2 {
		t.Fatalf("snapshot: %+v %v", current, err)
	}
	if previous.ProviderID != 1 {
		t.Fatal("in-flight snapshot mutated")
	}
	other, err := s.GetSettings(ctx, 2)
	if err != nil || other.ProviderID != 1 || other.Source != "generation" {
		t.Fatalf("other classroom: %+v %v", other, err)
	}
	if string(r.items[1].GenerationConfig) != `{"llm_provider_id":1,"llm_model_id":"chat"}` {
		t.Fatal("generation snapshot modified")
	}
}

func TestDiscussionSettingsRejectInvalidSelection(t *testing.T) {
	r := &discussionSettingsClassrooms{items: map[uint64]*entity.Classroom{1: {GenerationConfig: json.RawMessage(`{"llm_provider_id":1,"llm_model_id":"deleted"}`)}}}
	s := &discussionService{classrooms: r, modelCatalog: discussionSettingsCatalog{rows: []responsedto.AvailableLLMModel{{ProviderID: 2, ModelID: "chat"}}}}
	ctx := context.Background()
	for _, input := range []requestdto.DiscussionSettings{{}, {ProviderID: 1, ModelID: "chat"}, {ProviderID: 2, ModelID: " "}, {ProviderID: 2, ModelID: "deleted"}} {
		if _, err := s.UpdateSettings(ctx, 1, input); err == nil {
			t.Fatalf("accepted %+v", input)
		}
	}
	if r.saves != 0 {
		t.Fatal("invalid choice saved")
	}
	settings, err := s.GetSettings(ctx, 1)
	if err != nil || settings.Available || settings.ModelID != "deleted" {
		t.Fatalf("unavailable: %+v %v", settings, err)
	}
	if _, err := s.modelSnapshot(ctx, 1); err == nil {
		t.Fatal("deleted model silently replaced")
	}
	if _, err := s.GetSettings(ctx, 999); err == nil {
		t.Fatal("missing classroom accepted")
	}
	if _, err := s.UpdateSettings(ctx, 999, requestdto.DiscussionSettings{ProviderID: 2, ModelID: "chat"}); err == nil {
		t.Fatal("missing classroom updated")
	}
	r.saveErr = errors.New("write failed")
	if _, err := s.UpdateSettings(ctx, 1, requestdto.DiscussionSettings{ProviderID: 2, ModelID: "chat"}); err == nil {
		t.Fatal("write error ignored")
	}
	s.modelCatalog = discussionSettingsCatalog{err: errors.New("catalog failed")}
	if _, err := s.GetSettings(ctx, 1); err == nil {
		t.Fatal("catalog error ignored")
	}
}

func TestDiscussionSettingsNeverGuessesMissingModel(t *testing.T) {
	for _, raw := range []string{`{}`, `{"llm_provider_id":1}`, `{broken`} {
		r := &discussionSettingsClassrooms{items: map[uint64]*entity.Classroom{1: {GenerationConfig: json.RawMessage(raw)}}}
		s := &discussionService{classrooms: r}
		if _, err := s.modelSnapshot(context.Background(), 1); err == nil {
			t.Fatalf("accepted missing/invalid snapshot %s", raw)
		}
	}
}

func TestDiscussionModelSnapshotUsesClassroomSelection(t *testing.T) {
	classroom := &entity.Classroom{}
	if err := json.Unmarshal([]byte(`{"generation_config":{"llm_provider_id":1,"llm_model_id":"original"},"discussion_config":{"llm_provider_id":2,"llm_model_id":"selected"}}`), classroom); err != nil {
		t.Fatal(err)
	}
	svc := &discussionService{classrooms: &discussionSettingsClassrooms{items: map[uint64]*entity.Classroom{7: classroom}}}
	before := string(classroom.GenerationConfig)
	got, err := svc.modelSnapshot(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProviderID != 2 || got.ModelID != "selected" {
		t.Fatalf("discussion uses %+v, want provider 2 / selected", got)
	}
	if string(classroom.GenerationConfig) != before {
		t.Fatal("discussion selection changed the generation snapshot")
	}
}
