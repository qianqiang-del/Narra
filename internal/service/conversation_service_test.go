package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	requestdto "narra/internal/model/dto/request"
	"narra/internal/model/entity"
	"narra/internal/repository"
	apperrors "narra/pkg/errors"
)

type conversationTestClassrooms struct {
	item *entity.Classroom
	err  error
}

func (r conversationTestClassrooms) Create(context.Context, *entity.Classroom) error { return nil }
func (r conversationTestClassrooms) FindByID(context.Context, uint64) (*entity.Classroom, error) {
	return r.item, r.err
}
func (r conversationTestClassrooms) List(context.Context) ([]entity.Classroom, error) {
	return nil, nil
}
func (r conversationTestClassrooms) Delete(context.Context, uint64) error              { return nil }
func (r conversationTestClassrooms) UpdateTitle(context.Context, uint64, string) error { return nil }
func (r conversationTestClassrooms) UpdateStatus(context.Context, uint64, string, *string) error {
	return nil
}
func (r conversationTestClassrooms) SavePlan(context.Context, uint64, json.RawMessage, int32, string) error {
	return nil
}
func (r conversationTestClassrooms) UpdateRunID(context.Context, uint64, string) error { return nil }
func (r conversationTestClassrooms) ListGeneratingIDs(context.Context) ([]uint64, error) {
	return nil, nil
}

type conversationTestConversations struct {
	item      *entity.ClassroomConversation
	list      []entity.ClassroomConversation
	created   *entity.ClassroomConversation
	err       error
	listLimit int
}

func (r *conversationTestConversations) Create(_ context.Context, item *entity.ClassroomConversation) error {
	item.ID = 12
	r.created = item
	return r.err
}
func (r *conversationTestConversations) FindByID(context.Context, uint64) (*entity.ClassroomConversation, error) {
	return r.item, r.err
}
func (r *conversationTestConversations) ListByClassroom(_ context.Context, _ uint64, limit int) ([]entity.ClassroomConversation, error) {
	r.listLimit = limit
	return r.list, r.err
}
func (r *conversationTestConversations) TouchLastMessage(context.Context, uint64, time.Time) error {
	return nil
}
func (r *conversationTestConversations) Close(context.Context, uint64, time.Time) error { return nil }

type conversationTestMessages struct {
	items []entity.ConversationMessage
	after int64
	limit int
}

func (r *conversationTestMessages) AppendNext(context.Context, *entity.ConversationMessage) error {
	return nil
}
func (r *conversationTestMessages) AppendContent(context.Context, uint64, string) error { return nil }
func (r *conversationTestMessages) Finish(context.Context, uint64, string, int32) error { return nil }
func (r *conversationTestMessages) FindByID(context.Context, uint64) (*entity.ConversationMessage, error) {
	return nil, gorm.ErrRecordNotFound
}
func (r *conversationTestMessages) ListByConversation(_ context.Context, _ uint64, after int64, limit int) ([]entity.ConversationMessage, error) {
	r.after, r.limit = after, limit
	return r.items, nil
}
func (r *conversationTestMessages) CountByConversation(context.Context, uint64) (int64, error) {
	return 0, nil
}
func (r *conversationTestMessages) FailStreamingByRun(context.Context, uint64) error { return nil }

type conversationTestEvents struct{}

func (conversationTestEvents) ListAfter(context.Context, uint64, int64, int) ([]entity.ConversationEvent, error) {
	return nil, nil
}

func newConversationTestService(classrooms repository.ClassroomRepository, conversations repository.ConversationRepository, messages repository.MessageRepository) ConversationService {
	return NewConversationService(conversations, classrooms, messages, conversationTestEvents{})
}

func TestConversationServiceListMessagesPreservesSnapshotAndNormalizesLimit(t *testing.T) {
	conversations := &conversationTestConversations{item: &entity.ClassroomConversation{BaseModel: entity.BaseModel{ID: 42}, ClassroomID: 7}}
	messages := &conversationTestMessages{items: []entity.ConversationMessage{{
		BaseModel: entity.BaseModel{ID: 88}, ConversationID: 42, SequenceNo: 4, SenderType: entity.MessageSenderAgent,
		SenderSnapshot: json.RawMessage(`{"name":"陈老师"}`), Content: "先确认安全。", Status: entity.MessageStatusCompleted,
	}}}
	svc := newConversationTestService(conversationTestClassrooms{item: &entity.Classroom{BaseModel: entity.BaseModel{ID: 7}}}, conversations, messages)

	got, err := svc.ListMessages(context.Background(), 42, 3, 999)
	if err != nil {
		t.Fatalf("查询消息失败: %v", err)
	}
	if messages.after != 3 || messages.limit != 500 {
		t.Fatalf("分页参数 = after %d limit %d", messages.after, messages.limit)
	}
	if len(got) != 1 || string(got[0].SenderSnapshot) != `{"name":"陈老师"}` {
		t.Fatalf("快照未原样透传: %+v", got)
	}
}

func TestConversationServiceCreateValidatesClassroomAndInput(t *testing.T) {
	conversations := &conversationTestConversations{}
	svc := newConversationTestService(conversationTestClassrooms{item: &entity.Classroom{BaseModel: entity.BaseModel{ID: 7}}}, conversations, &conversationTestMessages{})
	item, err := svc.Create(context.Background(), 7, requestdto.CreateConversation{Title: " 新的讨论 ", Type: entity.ConversationTypeDiscussion})
	if err != nil || item == nil || item.Title != "新的讨论" || conversations.created == nil {
		t.Fatalf("创建结果不正确: item=%+v err=%v", item, err)
	}

	_, err = svc.Create(context.Background(), 7, requestdto.CreateConversation{Title: "", Type: entity.ConversationTypeDiscussion})
	var missing *apperrors.BizError
	if !errors.As(err, &missing) || missing.Code != apperrors.CodeMissingParam {
		t.Fatalf("空标题错误 = %v", err)
	}
	_, err = svc.Create(context.Background(), 7, requestdto.CreateConversation{Title: strings.Repeat("长", 201), Type: entity.ConversationTypeDiscussion})
	var invalid *apperrors.BizError
	if !errors.As(err, &invalid) || invalid.Code != apperrors.CodeInvalidParam {
		t.Fatalf("超长标题错误 = %v", err)
	}

	notFound := newConversationTestService(conversationTestClassrooms{err: gorm.ErrRecordNotFound}, conversations, &conversationTestMessages{})
	_, err = notFound.Create(context.Background(), 7, requestdto.CreateConversation{Title: "新的讨论", Type: entity.ConversationTypeDiscussion})
	var absent *apperrors.BizError
	if !errors.As(err, &absent) || absent.Code != apperrors.CodeNotFound {
		t.Fatalf("课堂不存在错误 = %v", err)
	}
}
