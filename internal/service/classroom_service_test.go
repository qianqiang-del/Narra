package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gorm.io/gorm"

	requestdto "narra/internal/model/dto/request"
	"narra/internal/model/entity"
	apperrors "narra/pkg/errors"
)

// 这个文件只测服务层的本职：课程材料引用的校验/归一化，以及它怎么落进生成配置。
// 材料的解析、切片与检索不在这里 —— 那些是知识库链路的既有能力。

// fakeMaterialReader 是 materialDocumentReader 的内存替身：只按 ID 找文档。
type fakeMaterialReader struct {
	documents map[uint64]*entity.KnowledgeDocument
	err       error
}

var _ materialDocumentReader = (*fakeMaterialReader)(nil)

func (r *fakeMaterialReader) GetByID(_ context.Context, id uint64) (*entity.KnowledgeDocument, error) {
	if r.err != nil {
		return nil, r.err
	}
	document, ok := r.documents[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return document, nil
}

func readyDocument(title string) *entity.KnowledgeDocument {
	return &entity.KnowledgeDocument{Title: title, Status: entity.KnowledgeDocumentStatusReady, Enabled: true}
}

func TestNormalizeMaterials(t *testing.T) {
	documents := map[uint64]*entity.KnowledgeDocument{
		7:  readyDocument("讲义标题"),
		8:  readyDocument("第二份"),
		9:  {Title: "处理中", Status: entity.KnowledgeDocumentStatusProcessing, Enabled: true},
		10: {Title: "已停用", Status: entity.KnowledgeDocumentStatusReady, Enabled: false},
	}

	overLimitReader := &fakeMaterialReader{documents: map[uint64]*entity.KnowledgeDocument{}}
	overLimitInput := make([]requestdto.CreateClassroomMaterial, 0, maxClassroomMaterials+1)
	for index := 0; index <= maxClassroomMaterials; index++ {
		id := uint64(100 + index)
		overLimitReader.documents[id] = readyDocument("材料")
		overLimitInput = append(overLimitInput, requestdto.CreateClassroomMaterial{DocumentID: id})
	}

	tests := []struct {
		name    string
		reader  materialDocumentReader
		input   []requestdto.CreateClassroomMaterial
		want    []requestdto.CreateClassroomMaterial
		wantErr int
	}{
		{
			name:   "空列表直接通过",
			reader: &fakeMaterialReader{documents: documents},
		},
		{
			name:   "合法材料透传并去掉名字空白",
			reader: &fakeMaterialReader{documents: documents},
			input:  []requestdto.CreateClassroomMaterial{{DocumentID: 7, Name: "  讲义.pdf  ", Size: 1024}},
			want:   []requestdto.CreateClassroomMaterial{{DocumentID: 7, Name: "讲义.pdf", Size: 1024}},
		},
		{
			name:   "名字为空回落到文档标题，负大小归零",
			reader: &fakeMaterialReader{documents: documents},
			input:  []requestdto.CreateClassroomMaterial{{DocumentID: 7, Size: -1}},
			want:   []requestdto.CreateClassroomMaterial{{DocumentID: 7, Name: "讲义标题", Size: 0}},
		},
		{
			name:    "缺文档 ID 拒绝",
			reader:  &fakeMaterialReader{documents: documents},
			input:   []requestdto.CreateClassroomMaterial{{Name: "无 ID"}},
			wantErr: apperrors.CodeBadRequest,
		},
		{
			name:    "重复文档拒绝",
			reader:  &fakeMaterialReader{documents: documents},
			input:   []requestdto.CreateClassroomMaterial{{DocumentID: 7}, {DocumentID: 7}},
			wantErr: apperrors.CodeBadRequest,
		},
		{
			name:    "文档不存在拒绝",
			reader:  &fakeMaterialReader{documents: documents},
			input:   []requestdto.CreateClassroomMaterial{{DocumentID: 999}},
			wantErr: apperrors.CodeBadRequest,
		},
		{
			name:    "还没收录完拒绝",
			reader:  &fakeMaterialReader{documents: documents},
			input:   []requestdto.CreateClassroomMaterial{{DocumentID: 9}},
			wantErr: apperrors.CodeBadRequest,
		},
		{
			name:    "已停用拒绝",
			reader:  &fakeMaterialReader{documents: documents},
			input:   []requestdto.CreateClassroomMaterial{{DocumentID: 10}},
			wantErr: apperrors.CodeBadRequest,
		},
		{
			name:    "超过数量上限拒绝",
			reader:  overLimitReader,
			input:   overLimitInput,
			wantErr: apperrors.CodeBadRequest,
		},
		{
			name:    "读文档出错翻内部错误",
			reader:  &fakeMaterialReader{err: errors.New("数据库挂了")},
			input:   []requestdto.CreateClassroomMaterial{{DocumentID: 7}},
			wantErr: apperrors.CodeInternalError,
		},
		{
			name:    "缺少知识库依赖拒绝",
			reader:  nil,
			input:   []requestdto.CreateClassroomMaterial{{DocumentID: 7}},
			wantErr: apperrors.CodeInternalError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &classroomService{materials: tt.reader}
			got, err := svc.normalizeMaterials(context.Background(), tt.input)
			if tt.wantErr != 0 {
				var biz *apperrors.BizError
				if !errors.As(err, &biz) || biz.Code != tt.wantErr {
					t.Fatalf("期望错误码 %d，实际 err=%v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("不该报错: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("归一化结果不对:\n got: %+v\nwant: %+v", got, tt.want)
			}
		})
	}
}

func TestBuildGenerationConfigCarriesMaterials(t *testing.T) {
	raw, err := buildGenerationConfig(requestdto.CreateClassroom{
		LLMProviderID: 3,
		LLMModelID:    "qwen-max",
		Materials: []requestdto.CreateClassroomMaterial{
			{DocumentID: 7, Name: "讲义.pdf", Size: 1024},
			{DocumentID: 8, Name: "笔记.md", Size: 256},
		},
	})
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	var config struct {
		Materials []requestdto.CreateClassroomMaterial `json:"materials"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if len(config.Materials) != 2 || config.Materials[0].DocumentID != 7 || config.Materials[1].Name != "笔记.md" {
		t.Fatalf("材料没有原样落进生成配置: %+v", config.Materials)
	}
}

func TestBuildGenerationConfigOmitsEmptyMaterials(t *testing.T) {
	raw, err := buildGenerationConfig(requestdto.CreateClassroom{LLMProviderID: 3, LLMModelID: "qwen-max"})
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if strings.Contains(string(raw), "materials") {
		t.Fatalf("没有材料时不该出现 materials 键: %s", raw)
	}
}
