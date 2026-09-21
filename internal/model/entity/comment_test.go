package entity

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// commentGuardModels 是要护栏守住的全部表实体，与 internal/app/app.go 里的
// AutoMigrate 清单是同一批（顺序无所谓，这里只关心集合）。
var commentGuardModels = []any{
	&Folder{},
	&Classroom{},
	&PresetAgent{},
	&ClassroomAgent{},
	&Scene{},
	&SceneSegment{},
	&EmbeddingModel{},
	&EmbeddingSetting{},
	&KnowledgeDocument{},
	&KnowledgeUploadRecord{},
	&KnowledgeChunk{},
	&KnowledgeEmbedding{},
	&ClassroomConversation{},
	&ConversationMessage{},
	&ContextCompaction{},
	&OrchestrationRun{},
	&AgentTurn{},
	&SharedContextMemory{},
	&ConversationEvent{},
	&AgentTraceSpan{},
	&MCPServer{},
	&LLMProvider{},
}

// tableNameMethod 用来在实体源码里找出「哪些结构体是表」。
var tableNameMethod = regexp.MustCompile(`func \(\w+\) TableName\(\) string`)

// 每一列都要有库注释。
//
// 库注释（gorm 的 comment tag，落库是 COMMENT ON COLUMN）是这套设计里最容易烂的一环：
// 结构全归实体 tag，加字段很顺手，注释却没人回头补，久了库里就是一堆裸列名 ——
// 而"表结构归实体 tag"恰好意味着**没有第二处**能补它。这条用例把自觉变成硬约束。
func TestEveryColumnHasDatabaseComment(t *testing.T) {
	checked := 0

	for _, model := range commentGuardModels {
		parsed := parseEntitySchema(t, model)
		columns := 0
		seen := map[string]bool{}

		for _, field := range parsed.Fields {
			if field.DBName == "" || field.IgnoreMigration || seen[field.DBName] {
				continue
			}
			// 按列名去重：遮蔽 BaseModel 的实体（如 KnowledgeDocument.CreatedAt）
			// 在 Fields 里同时留着嵌入字段和遮蔽字段两份，真正生效的是后者。
			// 不去重会把同一个列查两遍，数出来的列数也对不上库里的。
			seen[field.DBName] = true
			columns++

			if strings.TrimSpace(field.Comment) == "" {
				t.Errorf("%s.%s 没有库注释，请在 gorm tag 上补 comment:xxx",
					parsed.Table, field.DBName)
			}
		}

		// 防空转：schema 解析要是取不到字段，上面的循环一次都不进，用例会假绿。
		// 每张表至少得查到一个列，否则说明这道护栏根本没在工作。
		if columns == 0 {
			t.Errorf("%s 一个列都没查到，列注释检查形同虚设", parsed.Table)
		}
		checked += columns
	}

	t.Logf("已核对 %d 个列的库注释", checked)
}

// 护栏清单本身不能漏表。
//
// AutoMigrate 的清单在 app 包里，这里再抄一份必然会漂：新加一张表、只登记了一处，
// 上面那条覆盖率用例就会**静默放过**它（测不到的表永远不会失败）。所以直接扫源码，
// 把所有带 TableName() 的结构体找出来，与清单**双向**对账。
//
// 双向是必须的：只查"扫到的都在清单里"的话，万一正则哪天匹配不上（接收者写法变了），
// 扫到空集，用例照样绿 —— 一条永远不会失败的护栏等于没有护栏。所以反过来也要查：
// 清单里的每一张表都必须在源码里扫到。
func TestCommentGuardCoversEveryTable(t *testing.T) {
	declared := map[string]bool{}
	for _, model := range commentGuardModels {
		declared[reflect.TypeOf(model).Elem().Name()] = true
	}

	found := map[string]string{} // 类型名 -> 出处文件
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取实体目录失败: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", name, err)
		}
		for _, line := range strings.Split(string(source), "\n") {
			// 形如 func (KnowledgeDocument) TableName() string，取括号里的类型名。
			if !tableNameMethod.MatchString(line) {
				continue
			}
			start := strings.Index(line, "(")
			end := strings.Index(line, ")")
			if start < 0 || end <= start {
				continue
			}
			receiver := strings.TrimSpace(strings.TrimLeft(line[start+1:end], "*"))
			if receiver != "" {
				found[receiver] = name
			}
		}
	}

	for name := range found {
		if !declared[name] {
			t.Errorf("源码里的 %s（%s）是表，但没登记进 commentGuardModels，列注释检查覆盖不到它",
				name, found[name])
		}
	}
	for name := range declared {
		if _, ok := found[name]; !ok {
			t.Errorf("commentGuardModels 里的 %s 没在源码里扫到 TableName()，请核对扫描规则或清单", name)
		}
	}

	names := make([]string, 0, len(declared))
	for name := range declared {
		names = append(names, name)
	}
	sort.Strings(names)
	t.Logf("护栏覆盖 %d 张表: %s", len(names), strings.Join(names, ", "))
}
