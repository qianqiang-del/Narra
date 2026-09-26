package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"narra/internal/model/entity"
	"narra/pkg/utils"
)

// knowledgeInsertBatch 是切片与向量的批量插入粒度。
//
// 一次上传可能有上千个切片，逐条 INSERT 会把往返次数放大到无法忍受；
// 而一个批次也不能太大 —— 向量的文本形式单个就有几十 KB，
// 批次过大会撞上 PostgreSQL 单条语句的参数上限（65535 个占位符）。
// 200 条 × 2 列远在安全线内，同时把往返次数压到可接受的范围。
const knowledgeInsertBatch = 200

// knowledgeDocumentRepository 基于 GORM 的知识库仓储。
type knowledgeDocumentRepository struct {
	db *gorm.DB
}

// 本仓储的每个方法都通过 conn(ctx, r.db) 取句柄，而不是直接用 conn(ctx, r.db)：
// 收录链路把"建文档 + 写 metadata + 建上传记录"包在一个事务里（见 rag.Ingester.SubmitFile），
// 事务句柄由 repository.TransactionManager 放进 ctx。漏掉 conn 的方法会静默跑到事务外，
// 只在回滚场景才暴露 —— 这正是 tx.go 开头提醒的那种错误。

// SetMetadata 整份覆盖 metadata，不做合并（合并规则是调用方的事）。
func (r *knowledgeDocumentRepository) SetMetadata(ctx context.Context, id uint64, metadata json.RawMessage) error {
	return conn(ctx, r.db).Model(&entity.KnowledgeDocument{}).Where("id = ?", id).Update("metadata", metadata).Error
}

// SetUploadPath 把 metadata 里的 upload_path 换成失败原件归档后的新位置。
//
// 只处理**仍由这次失败持有**的文档：id、status = failed、ingest_attempt 三者全中才写。
// 不匹配说明已经有人点了重试（状态回到 pending / processing），或任务已被回收重新认领 ——
// 这时旧 Worker 的迟到归档绝不能改指针：它会污染新一轮的输入路径。
// 返回 applied = false 表示没有命中，调用方（worker）据此把已挪走的文件挪回原位。
//
// 单独开一个方法而不是复用 SetMetadata：后者是整份覆盖，而调用它的时机
// （归档失败原件）正好在 MarkFailed 刚写完失败现场之后 —— 用覆盖写法会把
// stage / error / failed_at 一起抹掉，用户就再也看不到这次为什么失败了。
//
// 与 MarkFailed 的"先读后写"不同，这里用一条 SQL 做 jsonb 顶层合并就够了：
// 只改一个键，不需要知道其余键是什么，也就不存在读到旧值再写回去的窗口。
func (r *knowledgeDocumentRepository) SetUploadPath(ctx context.Context, id uint64, attempt int32, path string) (bool, error) {
	result := conn(ctx, r.db).
		Model(&entity.KnowledgeDocument{}).
		Where("id = ? AND status = ? AND ingest_attempt = ?", id, entity.KnowledgeDocumentStatusFailed, attempt).
		Update("metadata", gorm.Expr("metadata || jsonb_build_object('upload_path', ?::text)", path))
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// FailedLeaseOwned 判断这一行是否仍由这次失败持有（status = failed 且租约编号相符）。
//
// 归档原件之前的最后一道核对：用户可能在失败现场写下的下一秒就点了重试，
// 新一轮已经接手并在读原文件。那时旧 Worker 必须彻底停手 —— 把文件挪走
// 会让新一轮读不到输入，这比"少归档一次"严重得多。
func (r *knowledgeDocumentRepository) FailedLeaseOwned(ctx context.Context, id uint64, attempt int32) (bool, error) {
	var row struct {
		Status        string
		IngestAttempt int32
	}
	if err := conn(ctx, r.db).
		Model(&entity.KnowledgeDocument{}).
		Select("status", "ingest_attempt").
		Where("id = ?", id).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return row.Status == entity.KnowledgeDocumentStatusFailed && row.IngestAttempt == attempt, nil
}

// ListPending 按创建时间正序取待处理文档（先进先出），只做查询、不改状态。
//
// 排序里带上 id 是因为同一批上传的 created_at 可能相同，只按时间排会让
// 前后两次取到的顺序不一致，任务可能被反复取到或被跳过。
func (r *knowledgeDocumentRepository) ListPending(ctx context.Context, limit int) ([]entity.KnowledgeDocument, error) {
	var documents []entity.KnowledgeDocument
	err := conn(ctx, r.db).Where("status = ?", entity.KnowledgeDocumentStatusPending).
		Order("created_at ASC, id ASC").Limit(limit).Find(&documents).Error
	return documents, err
}

// ClaimAndReturnAttempt 抢占一条待处理文档，并把租约编号原子递增后返回。
//
// 带 status = 'pending' 条件的 UPDATE，抢到才返回 claimed = true。
// 条件写在 UPDATE 的 WHERE 里而不是"先查再改"，是为了让并发下的取舍由数据库
// 一次性完成 —— 两个执行者同时来，只有一个能让 RowsAffected 为 1。
//
// 递增与置状态在同一条 UPDATE 里：编号就是"谁在跑"的凭据，分成两步写会留下
// 两个执行者拿到同一个编号的窗口。回读放在同一个事务里，读到的一定是自己刚写下的值。
//
// 用 Updates 传 map 而不是 UpdateColumn：claimed_at 这个语义要靠 updated_at 承担
// （ResetStale 按它判断僵尸任务），而 map 形式会触发 GORM 的 autoUpdateTime 自动带上它。
func (r *knowledgeDocumentRepository) ClaimAndReturnAttempt(ctx context.Context, id uint64) (int32, bool, error) {
	var attempt int32
	claimed := false

	err := conn(ctx, r.db).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&entity.KnowledgeDocument{}).
			Where("id = ? AND status = ?", id, entity.KnowledgeDocumentStatusPending).
			Updates(map[string]any{
				"status":         entity.KnowledgeDocumentStatusProcessing,
				"ingest_attempt": gorm.Expr("ingest_attempt + 1"),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		claimed = true

		var row struct{ IngestAttempt int32 }
		if err := tx.Model(&entity.KnowledgeDocument{}).
			Select("ingest_attempt").Where("id = ?", id).Take(&row).Error; err != nil {
			return err
		}
		attempt = row.IngestAttempt
		return nil
	})
	return attempt, claimed, err
}

// ResetStale 把卡在 processing 超过时限的文档打回 pending，让它们重新入队。
//
// 判据用 updated_at 而不是新增一列"开始处理时间"：Claim 与 MarkProcessing 都是
// 走 GORM 的 map 更新，会自动刷新 updated_at，所以它天然就是"最后一次有人动过"的时刻。
// 代价是别处不能再用 UpdateColumn 之类的写法绕过 autoUpdateTime，否则这里会误判。
// （唯一的例外是 Touch：它就是显式地"只推时间"，见那里的说明。）
//
// 判据的阈值必须与心跳间隔配套：Worker 处理期间每 30s 调一次 Touch，
// 所以 3 分钟的阈值既能很快收敛真僵尸，又不会误杀跑得慢的正常任务。
func (r *knowledgeDocumentRepository) ResetStale(ctx context.Context, olderThan time.Time) error {
	return conn(ctx, r.db).Model(&entity.KnowledgeDocument{}).
		Where("status = ? AND updated_at < ?", entity.KnowledgeDocumentStatusProcessing, olderThan).
		Updates(map[string]any{"status": entity.KnowledgeDocumentStatusPending}).Error
}

// Touch 把 processing 文档的 updated_at 推到当前时刻，作为"任务还活着"的心跳。
//
// 与 ResetStale 是一对：一个负责报活，一个负责回收没报活的。心跳停掉（进程被 kill、
// goroutine 消失）之后，行在 staleAfter 内就会变"旧"，被周期 ResetStale 捡回去 ——
// 不需要等下一次进程重启。
//
// 用 UpdateColumn + 显式时间：要的正是"只改这一列"。带 status 与租约编号条件是防御：
// 行已被删、已被 ResetStale 打回 pending、或已被重新认领时，这里影响 0 行且不报错 ——
// 心跳不该把任何状态改回去，也不该给旧租约续命（否则一个已经死掉的新任务会被
// 旧执行者一直"续命"，周期回收永远等不到它）。
func (r *knowledgeDocumentRepository) Touch(ctx context.Context, id uint64, attempt int32) error {
	return conn(ctx, r.db).
		Model(&entity.KnowledgeDocument{}).
		Where("id = ? AND status = ? AND ingest_attempt = ?", id, entity.KnowledgeDocumentStatusProcessing, attempt).
		UpdateColumn("updated_at", time.Now().UTC()).Error
}

// Delete 硬删除一篇文档，切片与向量由外键 ON DELETE CASCADE 带走。
//
// 走硬删除（实体没有 DeletedAt）而不是软删除，原因和 ReplaceChunks 里删旧切片一样：
// 留着旧行会让 UNIQUE (document_id, chunk_index) 挡住同一篇文章的重新导入。
func (r *knowledgeDocumentRepository) Delete(ctx context.Context, id uint64) error {
	return conn(ctx, r.db).Where("id = ?", id).Delete(&entity.KnowledgeDocument{}).Error
}

// NewKnowledgeDocumentRepository 创建知识库仓储。
func NewKnowledgeDocumentRepository(db *gorm.DB) KnowledgeDocumentRepository {
	return &knowledgeDocumentRepository{db: db}
}

// Create 插入一篇文档。实体里没有关联字段，所以不存在级联写入的副作用。
func (r *knowledgeDocumentRepository) Create(ctx context.Context, document *entity.KnowledgeDocument) error {
	return conn(ctx, r.db).Create(document).Error
}

// GetByID 按主键取一篇文档，查不到时把 gorm.ErrRecordNotFound 原样交给调用方，
// 由服务层翻成"文档不存在"。不在这里翻译成业务错误，是因为"记录不存在"在
// 收录链路（判断默认向量模型是否已登记）和 HTTP 面（404 语义）里的含义并不相同。
func (r *knowledgeDocumentRepository) GetByID(ctx context.Context, id uint64) (*entity.KnowledgeDocument, error) {
	var document entity.KnowledgeDocument
	if err := conn(ctx, r.db).Where("id = ?", id).First(&document).Error; err != nil {
		return nil, err
	}
	return &document, nil
}

// SetEnabled 单独切换一篇文档的检索开关，返回是否命中一行。
//
// 只改 enabled 一列：状态、切片与向量都不动 —— 停用只是让两条召回 SQL 过滤掉它
// （见 knowledge_search_repository），改回 true 立即恢复。
//
// 用 GORM 的 Update 而不是 UpdateColumn：updated_at 由 autoUpdateTime 维护
// （见 migrations/README.md 的对照表），这里必须跟着刷新。
func (r *knowledgeDocumentRepository) SetEnabled(ctx context.Context, id uint64, enabled bool) (bool, error) {
	result := conn(ctx, r.db).
		Model(&entity.KnowledgeDocument{}).
		Where("id = ?", id).
		Update("enabled", enabled)
	return result.RowsAffected > 0, result.Error
}

// List 按创建时间倒序分页，条件来自 entity.KnowledgeDocumentQuery。
//
// id 也参与排序，因为同一批导入的文档 created_at 可能相同，只按时间排会让翻页时
// 出现重复或漏项。
//
// 计数与取页共用同一组 WHERE：两者不同源的话，前端会收到"总数 3、本页 5 条"这种
// 自相矛盾的响应，而它正是靠 total 算"已显示 X / Y"和判断还有没有下一页的。
func (r *knowledgeDocumentRepository) List(ctx context.Context, query entity.KnowledgeDocumentQuery) ([]entity.KnowledgeDocument, int64, error) {
	scope := conn(ctx, r.db).Model(&entity.KnowledgeDocument{}).Scopes(documentConditions(query))

	var total int64
	if err := scope.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var documents []entity.KnowledgeDocument
	err := scope.Order("created_at DESC, id DESC").Offset(query.Offset).Limit(query.Limit).Find(&documents).Error
	if err != nil {
		return nil, 0, err
	}
	return documents, total, nil
}

// CountActive 统计还在收录中的文档数（pending + processing）。
//
// 它同时覆盖两种"忙"：worker 正在处理的行（processing），以及刚提交、还没被
// worker 取走的行（pending）。判据用库里的行数而不是进程内的标志位，是因为活的
// 执行者是 rag.Worker —— 提交请求早就返回了，只有文档状态知道后台还在不在忙，
// 进程重启后这一点依然成立。
//
// 这个查询吃得到 knowledge_documents_status_created_at_idx 这条部分索引：
// 它的谓词正是 status IN ('pending', 'processing')。
func (r *knowledgeDocumentRepository) CountActive(ctx context.Context) (int64, error) {
	var total int64
	err := conn(ctx, r.db).Model(&entity.KnowledgeDocument{}).
		Where("status IN ?", []string{
			entity.KnowledgeDocumentStatusPending,
			entity.KnowledgeDocumentStatusProcessing,
		}).
		Count(&total).Error
	return total, err
}

// ingestQueueLockKey 是收录队列容量检查的 PostgreSQL 咨询锁键。
//
// 取值本身没有含义，只要在整个数据库内稳定且不与别的咨询锁冲突即可（本项目目前
// 没有别处使用咨询锁）。十六进制拼的是 "narra" 的 ASCII，便于人工识别来源。
const ingestQueueLockKey = int64(0x6e61727261)

// AcquireIngestQueueLock 在**当前事务**里取得收录队列的排他锁。
//
// 它是"队列容量是硬上限"在多实例部署下的保证：CountActive 之后、插入新行之前的窗口
// 如果没有串行化，两个实例会同时看到还剩一个空位，各插一行，上限就被绕过。
// pg_advisory_xact_lock 在事务提交或回滚时自动释放，不需要（也不能）手工解锁；
// 调用方必须先通过 TransactionManager.Run 开事务，否则每条语句自成事务，
// 锁在执行完这一句后立刻释放，等于没锁（接口注释里也写了这条契约）。
//
// 用 Exec 而不是 Raw + Scan：这个函数只关心"锁有没有拿到"，返回值是 void。
func (r *knowledgeDocumentRepository) AcquireIngestQueueLock(ctx context.Context) error {
	return conn(ctx, r.db).Exec("SELECT pg_advisory_xact_lock(?)", ingestQueueLockKey).Error
}

// documentConditions 把查询条件翻成 WHERE 子句；条件为空时不加任何约束。
func documentConditions(query entity.KnowledgeDocumentQuery) func(*gorm.DB) *gorm.DB {
	return func(tx *gorm.DB) *gorm.DB {
		if len(query.Statuses) > 0 {
			tx = tx.Where("status IN ?", query.Statuses)
		}
		if keyword := strings.TrimSpace(query.Keyword); keyword != "" {
			pattern := likePattern(keyword)
			// 括号不能省：GORM 把多个 Where 用 AND 串起来，而 AND 比 OR 结合得紧，
			// 少了它这条会变成 "(status IN (…) AND title ILIKE …) OR source_uri ILIKE …"，
			// 后半句绕过了状态约束，会把别的状态的文档一起捞出来。
			tx = tx.Where("(title ILIKE ? OR source_uri ILIKE ?)", pattern, pattern)
		}
		return tx
	}
}

// likePattern 把用户输入包成 LIKE 模式，并转义模式里的通配符。
//
// 不转义的话，用户输入一个 % 就等于"匹配所有文档"，下划线同理 ——
// 界面上看起来像"搜索失效"，实际是把通配符的控制权交给了调用方。
// PostgreSQL 的 LIKE 默认转义字符就是反斜杠，所以这里补一个即可，不必写 ESCAPE 子句。
func likePattern(keyword string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(keyword)
	return "%" + escaped + "%"
}

// CountChunksByDocument 用一条 GROUP BY 查询批量取回切片数，避免列表页的 N+1。
func (r *knowledgeDocumentRepository) CountChunksByDocument(ctx context.Context, documentIDs []uint64) (map[uint64]int64, error) {
	counts := make(map[uint64]int64, len(documentIDs))
	if len(documentIDs) == 0 {
		return counts, nil
	}

	var rows []struct {
		DocumentID uint64
		Total      int64
	}
	err := conn(ctx, r.db).
		Model(&entity.KnowledgeChunk{}).
		Select("document_id, count(*) AS total").
		Where("document_id IN ?", documentIDs).
		Group("document_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		counts[row.DocumentID] = row.Total
	}
	return counts, nil
}

// MarkProcessing 推进到 processing。
//
// 用 Updates 传 map 而不是 UpdateColumn：map 形式的更新仍然会走 GORM 的
// autoUpdateTime 逻辑，updated_at 会被自动带上；UpdateColumn 会跳过它，
// 让这个字段永远停在创建时间（本项目的 updated_at 没有数据库触发器兜底）。
func (r *knowledgeDocumentRepository) MarkProcessing(ctx context.Context, id uint64) error {
	return conn(ctx, r.db).
		Model(&entity.KnowledgeDocument{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": entity.KnowledgeDocumentStatusProcessing}).Error
}

// MarkFailed 在一个事务里把文档推进到 failed，把失败现场**合并**进 metadata，
// 并把原因同步到这次上传的记录上。返回 applied 表示失败现场是否真的写进了这一行。
//
// 两行必须一起改：文档状态是给主页看的，记录状态是给上传记录抽屉看的 ——
// 只改一处会让同一件事在两个界面上说法不同。
//
// 只处理**仍在 processing、且租约编号相符**的文档：带 status 与 ingest_attempt 条件
// 而不是裸 id，是为了不覆盖别人已经推进到的状态，也挡住旧执行者的迟到写入。
// 最典型的两个场景：用户在后台处理期间把这条记录（连同文档）删掉了；或者旧 Worker
// 被 ResetStale 回收、任务已被重新认领 —— 两种情况下 UPDATE 都影响 0 行，
// 这里返回 applied = false 且不写任何失败现场（那行已经不属于这次处理了）。
// 调用方（rag.Worker）据此决定不归档原件：把新一轮正在用的文件挪走会打断新任务。
//
// 数据库层面的错误以 err 返回（applied 同样是 false），调用方按"这次失败没能落库"
// 处理，日志里要留下痕迹。
//
// metadata 是合并写回而不是整份覆盖。这张表的 metadata 同时装着两件事：
// "这一次失败长什么样"（stage / error / failed_at，由收录链路写）与
// "收录这份文件的输入"（upload_path 指向磁盘上的原件、explicit_title 记着标题要不要
// 回落到正文标题，由 SubmitFile 写）。覆盖会让后者整体消失，而后果不只是少几个字段：
// upload_path 没了，失败原件的归档与删除时的清理都找不到它 —— 库里留一行 failed、
// 磁盘上留一份没人认领的文件，就这么攒出了孤儿。
//
// 先读后写放在事务里：调用者是已经抢到这一行的执行者（rag.Worker 的 ClaimAndReturnAttempt），
// 同一行不存在第二个写者，所以不需要行锁。
//
// reason 是给用户看的那一句中文（"文档解析失败：解析环境缺少 Python 模块 scipy"），
// 由调用方从错误里折出来后显式传进来 —— 界面上显示的就是它，不是诊断串。
// 仓储不去解析 metadata 的 JSON 结构：那个结构是收录链路
// 与接口层共享的约定，在这里再实现一遍就成了第三份口径。
func (r *knowledgeDocumentRepository) MarkFailed(ctx context.Context, id uint64, attempt int32, metadata json.RawMessage, reason string) (bool, error) {
	applied := false

	err := conn(ctx, r.db).Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{"status": entity.KnowledgeDocumentStatusFailed}
		if len(metadata) > 0 {
			base, err := readMetadata(tx, id)
			if err != nil {
				return err
			}
			updates["metadata"] = mergeMetadata(base, metadata)
		}

		result := tx.Model(&entity.KnowledgeDocument{}).
			Where("id = ? AND status = ? AND ingest_attempt = ?", id, entity.KnowledgeDocumentStatusProcessing, attempt).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			// 行没了、已被推走、或租约已失效：整笔不写，包括下面那条记录更新。
			// 记录不去单独更新是对的 —— 文档既然已不属于这次处理，记录要么一起没了，
			// 要么已经由新一轮任务在管。
			return nil
		}
		applied = true

		// 手动录入（IngestText）没有上传记录，这里匹配 0 行，不报错也不影响什么。
		return tx.Model(&entity.KnowledgeUploadRecord{}).
			Where("document_id = ?", id).
			Updates(map[string]any{
				"status":        entity.KnowledgeUploadRecordStatusFailed,
				"error_message": utils.OptionalString(reason),
			}).Error
	})
	return applied, err
}

// readMetadata 读一篇文档当前的 metadata，供合并写回使用。
//
// 行不存在时返回 nil 而不是 ErrRecordNotFound：调用方随后那条带状态条件的 UPDATE
// 会自己判断"这一行还在不在、属不属于我"，读取这一步只需要把旧值带出来。
func readMetadata(tx *gorm.DB, id uint64) (json.RawMessage, error) {
	var current entity.KnowledgeDocument
	err := tx.Model(&entity.KnowledgeDocument{}).
		Select("metadata").Where("id = ?", id).Take(&current).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return current.Metadata, nil
}

// Requeue 把一行 failed 文档改回 pending 重新排队，清掉上一次的失败现场，
// 并把 ingest_stage 改写成按现实材料算出的恢复点（由调用方算好传入）。
//
// 这是"原地重试"的写入口：复用同一行文档、同一条上传记录，不新建任何东西 ——
// 一份文件一份资产，重试只是让它再跑一遍。返回 false 表示这一行不满足条件
// （不存在，或状态已经不是 failed 了），调用方据此报"不需要重试"。
//
// 条件写在 UPDATE 的 WHERE 里而不是先查再改：两个请求同时点重试时，
// 只有一个的 RowsAffected 会是 1，另一个拿到 false —— 不需要额外的锁。
//
// 阶段在这里被改写而不是保留原值：重试的起点由"现实还剩什么材料"决定
// （有切片直接重向量化，没切片有正文重分块，都没了才重新解析原文件），
// 死守原来那个阶段会在材料被动过时走进死胡同。下一轮 Worker 还会再算一次，
// 防止"点了重试之后材料又没了"。
//
// 清掉的是 metadata 里的 stage / error / failed_at 三个键，它们描述的是上一次：
// 留着会让处理期间的前端一直读到上一轮的失败原因。而 upload_path 与 explicit_title
// 必须保留 —— 前者是这次重试的输入（原件已归档到 failed/<文档ID>/ 下），
// 后者决定标题要不要回落到正文标题，丢了会让重试后的标题与第一次不一致。
//
// 同一个事务里把上传记录也置回 pending 并清掉 error_message：抽屉里显示的是它。
// 只改文档不改记录，用户会看到"文档在转圈、记录那一行还说失败"。
func (r *knowledgeDocumentRepository) Requeue(ctx context.Context, id uint64, stage string) (bool, error) {
	requeued := false
	err := conn(ctx, r.db).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&entity.KnowledgeDocument{}).
			Where("id = ? AND status = ?", id, entity.KnowledgeDocumentStatusFailed).
			Updates(map[string]any{
				"status":       entity.KnowledgeDocumentStatusPending,
				"ingest_stage": stage,
				"metadata":     gorm.Expr("metadata - 'stage' - 'error' - 'failed_at'"),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		requeued = true

		// 手动录入（IngestText）没有上传记录，这里匹配 0 行，无害。
		return tx.Model(&entity.KnowledgeUploadRecord{}).
			Where("document_id = ?", id).
			Updates(map[string]any{
				"status":        entity.KnowledgeUploadRecordStatusPending,
				"error_message": nil,
			}).Error
	})
	return requeued, err
}

// SaveParsedContent 保存解析产物，并把 ingest_stage 推进到 chunk。
//
// 正文与阶段必须原子：只写正文不推阶段会让恢复重新解析（浪费但安全），
// 只推阶段不写正文会让恢复读到空正文（数据丢失，不可接受）。metadata 走顶层合并，
// upload_path 与 explicit_title 原样保留 —— 进程崩溃后靠它找回原件。
//
// 只处理仍在 processing 且租约编号相符的文档，见 stagedUpdate。
func (r *knowledgeDocumentRepository) SaveParsedContent(ctx context.Context, id uint64, attempt int32, input entity.ParsedContent) error {
	if strings.TrimSpace(input.Content) == "" {
		return fmt.Errorf("文档 %d 的解析正文为空，不能落库", id)
	}

	return conn(ctx, r.db).Transaction(func(tx *gorm.DB) error {
		base, err := readMetadata(tx, id)
		if err != nil {
			return err
		}

		updates := map[string]any{
			"title":            input.Title,
			"content":          input.Content,
			"content_checksum": input.Checksum,
			"ingest_stage":     entity.KnowledgeDocumentStageChunk,
		}
		if len(input.Metadata) > 0 {
			updates["metadata"] = mergeMetadata(base, input.Metadata)
		}
		return stagedUpdate(tx, id, attempt, updates, "解析正文")
	})
}

// ReplaceStagedChunks 换掉这篇文档的全部切片，并把 ingest_stage 推进到 embed。
//
// 顺序不能反：先删旧切片再插新切片（UNIQUE (document_id, chunk_index) 要求同序号
// 的上一条先消失；旧向量由外键级联带走），最后才推进阶段 —— stage = embed 的语义
// 就是"切片已落库"，反过来会出现一个恢复时读不到切片的 embed 阶段。
//
// 不写 content：走到这一步的文档，正文在 SaveParsedContent 时已经落库。
func (r *knowledgeDocumentRepository) ReplaceStagedChunks(ctx context.Context, id uint64, attempt int32, chunks []entity.KnowledgeChunk, metadata json.RawMessage) error {
	if len(chunks) == 0 {
		return fmt.Errorf("文档 %d 没有可写入的切片", id)
	}

	return conn(ctx, r.db).Transaction(func(tx *gorm.DB) error {
		// 1. 先删旧切片。硬删除，同序号的上一条必须先消失，否则重复导入会撞唯一约束。
		if err := tx.Where("document_id = ?", id).Delete(&entity.KnowledgeChunk{}).Error; err != nil {
			return err
		}

		// 2. 批量插入切片。关联字段保持 nil，GORM 对 nil 的 belongs-to 关联不做级联写入。
		if err := tx.CreateInBatches(chunks, knowledgeInsertBatch).Error; err != nil {
			return err
		}

		// 3. 推进阶段并合并 metadata（chunks / chunk_ms 这些处理产物）。
		base, err := readMetadata(tx, id)
		if err != nil {
			return err
		}
		updates := map[string]any{"ingest_stage": entity.KnowledgeDocumentStageEmbed}
		if len(metadata) > 0 {
			updates["metadata"] = mergeMetadata(base, metadata)
		}
		return stagedUpdate(tx, id, attempt, updates, "切片")
	})
}

// ListChunksByDocument 按 chunk_index 升序取回一篇文档的全部切片。
//
// 顺序就是向量与切片的对应顺序：embed 阶段按它恢复输入，保存向量时按同一个顺序回填
// ChunkID，错位会让"第 3 段的向量"指向第 5 段，而检索看起来一切正常。
func (r *knowledgeDocumentRepository) ListChunksByDocument(ctx context.Context, id uint64) ([]entity.KnowledgeChunk, error) {
	var chunks []entity.KnowledgeChunk
	err := conn(ctx, r.db).
		Where("document_id = ?", id).
		Order("chunk_index ASC").
		Find(&chunks).Error
	return chunks, err
}

// SaveEmbeddingsAndMarkReady 写入向量并把文档置为 ready、ingest_stage 置 NULL。
//
// 必须是一个事务：只有全部向量写成功，文档才能 ready。分开提交会留下
// "可检索但没有向量"的文档 —— 界面上看不出任何异常，检索却永远漏掉它。
//
// 向量与切片要双向校验：数量相等挡不住张冠李戴（向量挂在别的文档的切片上），
// 归属相符也挡不住漏写。两者都过之后才写。先清旧向量再写，重复执行幂等
// （也避免撞 UNIQUE (chunk_id, model_id)）。
func (r *knowledgeDocumentRepository) SaveEmbeddingsAndMarkReady(ctx context.Context, id uint64, attempt int32, embeddings []entity.KnowledgeEmbedding, metadata json.RawMessage) error {
	if len(embeddings) == 0 {
		return fmt.Errorf("文档 %d 没有可写入的向量", id)
	}

	return conn(ctx, r.db).Transaction(func(tx *gorm.DB) error {
		chunkIDs := make([]uint64, len(embeddings))
		for index := range embeddings {
			chunkIDs[index] = embeddings[index].ChunkID
		}

		var chunkCount int64
		if err := tx.Model(&entity.KnowledgeChunk{}).Where("document_id = ?", id).Count(&chunkCount).Error; err != nil {
			return err
		}
		if int(chunkCount) != len(embeddings) {
			return fmt.Errorf("文档 %d 的切片数 %d 与向量数 %d 不一致", id, chunkCount, len(embeddings))
		}

		var matched int64
		if err := tx.Model(&entity.KnowledgeChunk{}).
			Where("document_id = ? AND id IN ?", id, chunkIDs).
			Count(&matched).Error; err != nil {
			return err
		}
		if int(matched) != len(chunkIDs) {
			return fmt.Errorf("文档 %d 的向量与切片对不上：%d 个向量里只有 %d 个属于本文档的切片", id, len(chunkIDs), matched)
		}

		if err := tx.Where("chunk_id IN ?", chunkIDs).Delete(&entity.KnowledgeEmbedding{}).Error; err != nil {
			return err
		}
		if err := tx.CreateInBatches(embeddings, knowledgeInsertBatch).Error; err != nil {
			return err
		}

		base, err := readMetadata(tx, id)
		if err != nil {
			return err
		}
		updates := map[string]any{
			"status":       entity.KnowledgeDocumentStatusReady,
			"ingest_stage": nil,
		}
		if len(metadata) > 0 {
			updates["metadata"] = mergeMetadata(base, metadata)
		}
		if err := stagedUpdate(tx, id, attempt, updates, "向量"); err != nil {
			return err
		}

		// 这次投递成功了，记录也跟着到 ready。必须在同一个事务里：
		// 否则会出现"文档已经可检索、记录还停在处理中"这两行互相矛盾的状态。
		// 手动录入（IngestText）没有记录，这里匹配 0 行，无害。
		return tx.Model(&entity.KnowledgeUploadRecord{}).
			Where("document_id = ?", id).
			Update("status", entity.KnowledgeUploadRecordStatusReady).Error
	})
}

// stagedUpdate 是分阶段写入共用的收尾：带状态与租约条件更新文档行。
//
// 影响 0 行时返回错误（整笔回滚）。这不是"内部错误"，而是这次处理已经失去写权限：
// 行被删了、被 ResetStale 打回 pending、或已被重新认领。调用方（rag.failIngest）
// 会把原因记进日志，但不会把这次失败写成文档的 failed —— 那行已经不属于它了。
func stagedUpdate(tx *gorm.DB, id uint64, attempt int32, updates map[string]any, what string) error {
	result := tx.Model(&entity.KnowledgeDocument{}).
		Where("id = ? AND status = ? AND ingest_attempt = ?", id, entity.KnowledgeDocumentStatusProcessing, attempt).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("文档 %d 不存在、已不在处理中或租约已失效，%s未写入", id, what)
	}
	return nil
}

// mergeMetadata 把 payload 的顶层键合并到 base 上，同名的键以 payload 为准。
//
// 只做顶层合并：这张表的 metadata 里都是扁平键（upload_path / explicit_title /
// stage / error / failed_at / parser / chunks / model …），没有需要递归的嵌套对象。
//
// 任一侧读不成 JSON 对象时都退回 payload：那种情况下没有可合并的东西，
// 写进这次失败现场比留下一份读不出来的旧值有用。
func mergeMetadata(base, payload json.RawMessage) json.RawMessage {
	merged := map[string]any{}
	if len(base) > 0 {
		if err := json.Unmarshal(base, &merged); err != nil {
			merged = map[string]any{}
		}
	}

	var incoming map[string]any
	if err := json.Unmarshal(payload, &incoming); err != nil {
		return payload
	}
	for key, value := range incoming {
		merged[key] = value
	}

	out, err := json.Marshal(merged)
	if err != nil {
		return payload
	}
	return out
}

// ReplaceChunks 在一个事务里替换切片与向量，并把文档置为 ready。
func (r *knowledgeDocumentRepository) ReplaceChunks(ctx context.Context, id uint64, input entity.ChunkReplacement) error {
	if len(input.Chunks) != len(input.Embeddings) {
		return fmt.Errorf("切片与向量数量不一致: %d 个切片 / %d 个向量", len(input.Chunks), len(input.Embeddings))
	}
	if len(input.Chunks) == 0 {
		// 一篇 ready 但没有任何切片的文档，在检索里等同于不存在，
		// 但它会占着列表、让用户以为已经入库成功。
		return fmt.Errorf("文档 %d 没有可写入的切片", id)
	}

	return conn(ctx, r.db).Transaction(func(tx *gorm.DB) error {
		// 1. 先删旧切片。删除走的是硬删除（实体没有 DeletedAt），
		//    这正是需要的效果：UNIQUE (document_id, chunk_index) 要求同序号的上一条
		//    必须先消失，软删除会让重复导入同一篇文章直接撞唯一约束。
		//    旧向量不需要显式删：knowledge_embeddings 的外键是 ON DELETE CASCADE。
		if err := tx.Where("document_id = ?", id).Delete(&entity.KnowledgeChunk{}).Error; err != nil {
			return err
		}

		// 2. 批量插入切片。必须插完才知道自增主键 —— 向量的 chunk_id 依赖它，
		//    所以这里不能用返回 ID 之外的方式省这一步。
		//    两个实体的关联字段（Chunk / Model / Document）都保持 nil，
		//    GORM 对 nil 的 belongs-to 关联不会做任何级联写入。
		if err := tx.CreateInBatches(input.Chunks, knowledgeInsertBatch).Error; err != nil {
			return err
		}
		for index := range input.Embeddings {
			input.Embeddings[index].ChunkID = input.Chunks[index].ID
		}
		if err := tx.CreateInBatches(input.Embeddings, knowledgeInsertBatch).Error; err != nil {
			return err
		}

		// 3. 最后更新文档本身。这一步必须在切片写好之后、且在同一个事务里：
		//    status = 'ready' 的 CHECK 约束要求 content 非空，而"内容已经落库"
		//    与"文档标记为可检索"如果分开提交，中间失败会留下一个 ready
		//    却没有切片的文档 —— 用户看到入库成功，检索却永远搜不到它。
		//
		//    带 status = 'processing' 条件：文档可能在这次处理期间被用户删掉，
		//    或者被别的执行者推进到了别的状态。这时影响 0 行，返回错误让整个事务回滚 ——
		//    绝不能给一个不在处理中的文档写入切片（那会留下没有归属的数据，
		//    以及磁盘上一份没人认领的失败原件）。
		updates := map[string]any{
			"title":            input.Title,
			"content":          input.Content,
			"content_checksum": input.Checksum,
			"status":           entity.KnowledgeDocumentStatusReady,
			// 同步链路也走这条不变量：ready 文档没有下一步，阶段必须清空，
			// 否则会出现 ready + ingest_stage = parse 这种没人定义过的组合。
			"ingest_stage": nil,
		}
		if len(input.Metadata) > 0 {
			updates["metadata"] = input.Metadata
		}
		result := tx.Model(&entity.KnowledgeDocument{}).
			Where("id = ? AND status = ?", id, entity.KnowledgeDocumentStatusProcessing).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("文档 %d 不存在或已不在处理中，本次切片写入已回滚", id)
		}

		// 4. 这次投递成功了，记录也跟着到 ready。
		//    必须在同一个事务里：否则会出现"文档已经可检索、记录还停在处理中"这两行
		//    互相矛盾的状态，用户看到的就是抽屉里一条永远转圈的处理中。
		//    手动录入（IngestText）没有记录，这里匹配 0 行，无害。
		return tx.Model(&entity.KnowledgeUploadRecord{}).
			Where("document_id = ?", id).
			Update("status", entity.KnowledgeUploadRecordStatusReady).Error
	})
}
