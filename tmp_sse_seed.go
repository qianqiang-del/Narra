// 临时冒烟脚本：造一条对话 + 三条事件，供 curl 验证 SSE 端点；用完 -cleanup 删掉。
// 验证完即删，不属于项目代码。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"

	"narra/internal/model/entity"
	"narra/internal/repository"
	"narra/pkg/config"
	"narra/pkg/database"
	"narra/pkg/logger"
)

func main() {
	cleanupID := flag.Uint64("cleanup", 0, "删除指定对话及其临时课程")
	flag.Parse()

	cfg, err := config.Load("configs/config.yaml")
	if err != nil {
		log.Fatal(err)
	}
	if err := logger.Init(&cfg.Log); err != nil {
		log.Fatal(err)
	}
	db, err := database.InitPostgres(&cfg.Database.Postgres)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()

	if *cleanupID != 0 {
		var conversation entity.ClassroomConversation
		if err := db.WithContext(ctx).First(&conversation, *cleanupID).Error; err != nil {
			log.Fatal(err)
		}
		db.WithContext(ctx).Where("conversation_id = ?", conversation.ID).Delete(&entity.ConversationEvent{})
		db.WithContext(ctx).Where("id = ?", conversation.ID).Delete(&entity.ClassroomConversation{})
		db.WithContext(ctx).Where("id = ?", conversation.ClassroomID).Delete(&entity.Classroom{})
		fmt.Printf("cleaned conversation_id=%d\n", conversation.ID)
		return
	}

	classroom := &entity.Classroom{
		Title:            "SSE 冒烟（临时，验证后删除）",
		Requirement:      "临时数据",
		Mode:             entity.ClassroomModeInteractive,
		Status:           entity.ClassroomStatusPlayable,
		GenerationConfig: json.RawMessage("{}"),
		AgentConfig:      json.RawMessage("{}"),
	}
	if err := db.WithContext(ctx).Create(classroom).Error; err != nil {
		log.Fatal(err)
	}
	conversation := &entity.ClassroomConversation{
		ClassroomID: classroom.ID,
		Title:       "SSE 冒烟",
		Type:        entity.ConversationTypeDiscussion,
		Status:      entity.ConversationStatusActive,
	}
	if err := db.WithContext(ctx).Create(conversation).Error; err != nil {
		log.Fatal(err)
	}

	events := repository.NewConversationEventRepository(db)
	tx := repository.NewTransactionManager(db)
	frames := []struct{ eventType, payload string }{
		{entity.ConversationEventRunStarted, `{"max_turns":3}`},
		{entity.ConversationEventMessageDelta, `{"delta":"你好，这是冒烟数据"}`},
		{entity.ConversationEventRunCompleted, `{"stop_reason":"completed"}`},
	}
	for _, frame := range frames {
		event := &entity.ConversationEvent{
			ConversationID: conversation.ID,
			EventType:      frame.eventType,
			Payload:        json.RawMessage(frame.payload),
		}
		if err := tx.Run(ctx, func(ctx context.Context) error {
			return events.AppendNext(ctx, event)
		}); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Printf("conversation_id=%d\n", conversation.ID)
}
