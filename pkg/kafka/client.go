// Package kafka 提供了与 Kafka 消息队列交互的功能。
package kafka

import (
	"code-agent/internal/model"
	"code-agent/internal/repository"
	"code-agent/internal/serverconfig"
	"code-agent/pkg/log"
	"code-agent/pkg/tasks"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
)

// TaskProcessor defines the interface for any service that can process a task.
type TaskProcessor interface {
	Process(ctx context.Context, task tasks.FileProcessingTask) error
}

// topicSet represents a topic set.
type topicSet struct {
	parse string
	chunk string
	embed string
	index string
	dlq   string
}

var (
	writers        map[string]*kafka.Writer
	topics         topicSet
	producerCfg    serverconfig.KafkaConfig
	producerDialer *kafka.Dialer
)

// normalizeKafkaConfig normalizes kafka config.
func normalizeKafkaConfig(cfg serverconfig.KafkaConfig) serverconfig.KafkaConfig {
	if cfg.ConsumerGroupPrefix == "" {
		cfg.ConsumerGroupPrefix = "codeagent"
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	if cfg.BaseBackoffMs <= 0 {
		cfg.BaseBackoffMs = 800
	}
	if cfg.EmbeddingBatchSize <= 0 {
		cfg.EmbeddingBatchSize = 8
	}
	if cfg.ESBulkBatchSize <= 0 {
		cfg.ESBulkBatchSize = 100
	}
	if cfg.Topics.Parse == "" {
		if cfg.Topic != "" {
			cfg.Topics.Parse = cfg.Topic
		} else {
			cfg.Topics.Parse = "file-parse"
		}
	}
	if cfg.Topics.Chunk == "" {
		cfg.Topics.Chunk = "file-chunk"
	}
	if cfg.Topics.Embed == "" {
		cfg.Topics.Embed = "file-embed"
	}
	if cfg.Topics.Index == "" {
		cfg.Topics.Index = "file-index"
	}
	if cfg.Topics.DLQ == "" {
		cfg.Topics.DLQ = "file-dlq"
	}
	return cfg
}

// InitProducer initializes writers for all pipeline topics.
func InitProducer(cfg serverconfig.KafkaConfig) {
	cfg = normalizeKafkaConfig(cfg)
	producerCfg = cfg
	brokers := parseKafkaBrokers(cfg.Brokers)
	dialer := &kafka.Dialer{
		Timeout:   10 * time.Second,
		DualStack: true,
		KeepAlive: 30 * time.Second,
	}
	producerDialer = dialer
	topics = topicSet{
		parse: cfg.Topics.Parse,
		chunk: cfg.Topics.Chunk,
		embed: cfg.Topics.Embed,
		index: cfg.Topics.Index,
		dlq:   cfg.Topics.DLQ,
	}
	writers = make(map[string]*kafka.Writer, 5)
	for _, t := range []string{topics.parse, topics.chunk, topics.embed, topics.index, topics.dlq} {
		if _, ok := writers[t]; ok {
			continue
		}
		writers[t] = &kafka.Writer{
			Addr:         kafka.TCP(brokers...),
			Topic:        t,
			Balancer:     &kafka.LeastBytes{},
			RequiredAcks: kafka.RequireOne,
			MaxAttempts:  maxInt(cfg.MaxRetries, 3),
			BatchTimeout: 150 * time.Millisecond,
			ReadTimeout:  20 * time.Second,
			WriteTimeout: 20 * time.Second,
			Transport: &kafka.Transport{
				Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
					return dialer.DialContext(ctx, network, address)
				},
			},
		}
	}
	log.Infof("Kafka 生产者初始化成功, topics=%v", []string{topics.parse, topics.chunk, topics.embed, topics.index, topics.dlq})
}

// topicByStage handles topic by stage.
func topicByStage(stage tasks.Stage) string {
	switch stage {
	case tasks.StageParse:
		return topics.parse
	case tasks.StageChunk:
		return topics.chunk
	case tasks.StageEmbed:
		return topics.embed
	case tasks.StageIndex:
		return topics.index
	default:
		return topics.dlq
	}
}

// produceToTopic handles produce to topic.
func produceToTopic(ctx context.Context, topic string, task tasks.FileProcessingTask) error {
	if writers == nil {
		return errors.New("kafka producer not initialized")
	}
	writer, ok := writers[topic]
	if !ok {
		return fmt.Errorf("kafka writer for topic '%s' not found", topic)
	}
	taskBytes, err := json.Marshal(task)
	if err != nil {
		return err
	}

	maxAttempts := maxInt(producerCfg.MaxRetries, 3)
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		writeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err = writer.WriteMessages(writeCtx, kafka.Message{
			Time:  time.Now(),
			Value: taskBytes,
		})
		cancel()
		if err == nil {
			return nil
		}

		lastErr = err
		log.Warnf("Kafka writer fallback probe, topic=%s attempt=%d/%d err=%v", topic, attempt, maxAttempts, err)
		if leaderErr := produceByLeaderDial(ctx, topic, taskBytes); leaderErr == nil {
			log.Infof("Kafka leader dial fallback succeeded, topic=%s attempt=%d/%d", topic, attempt, maxAttempts)
			return nil
		} else {
			lastErr = leaderErr
			log.Warnf("Kafka leader dial fallback failed, topic=%s attempt=%d/%d err=%v", topic, attempt, maxAttempts, leaderErr)
		}
		if attempt == maxAttempts || !isRetriableProduceError(err) {
			break
		}

		backoff := time.Duration(maxInt(producerCfg.BaseBackoffMs, 500)) * time.Millisecond * time.Duration(1<<(attempt-1))
		if backoff > 5*time.Second {
			backoff = 5 * time.Second
		}
		log.Warnf("Kafka produce retry, topic=%s attempt=%d/%d backoff=%s err=%v", topic, attempt, maxAttempts, backoff, err)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}

	return lastErr
}

// produceByLeaderDial handles produce by leader dial.
func produceByLeaderDial(ctx context.Context, topic string, taskBytes []byte) error {
	if producerDialer == nil {
		return errors.New("kafka producer dialer not initialized")
	}

	brokers := parseKafkaBrokers(producerCfg.Brokers)
	var lastErr error
	for _, broker := range brokers {
		writeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		conn, err := producerDialer.DialLeader(writeCtx, "tcp", broker, topic, 0)
		cancel()
		if err != nil {
			lastErr = err
			continue
		}

		_ = conn.SetWriteDeadline(time.Now().Add(20 * time.Second))
		_, err = conn.WriteMessages(kafka.Message{
			Time:  time.Now(),
			Value: taskBytes,
		})
		_ = conn.Close()
		if err == nil {
			return nil
		}
		lastErr = err
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("failed to dial kafka leader for topic=%s", topic)
	}
	return lastErr
}

// ProduceFileTask enqueues the first stage of the pipeline (parse).
func ProduceFileTask(task tasks.FileProcessingTask) error {
	if task.Stage == "" {
		task.Stage = tasks.StageParse
	}
	return produceToTopic(context.Background(), topicByStage(task.Stage), task)
}

// ProduceTask handles produce task.
func ProduceTask(task tasks.FileProcessingTask) error {
	return produceToTopic(context.Background(), topicByStage(task.Stage), task)
}

// ProduceTaskToDLQ handles produce task to dlq.
func ProduceTaskToDLQ(task tasks.FileProcessingTask) error {
	return produceToTopic(context.Background(), topics.dlq, task)
}

// StartPipelineConsumers starts one consumer for each stage topic.
func StartPipelineConsumers(cfg serverconfig.KafkaConfig, processor TaskProcessor, tracker repository.PipelineTaskRepository) {
	cfg = normalizeKafkaConfig(cfg)
	go consumeStage(cfg, tracker, processor, tasks.StageParse, cfg.Topics.Parse, cfg.ConsumerGroupPrefix+"-parse")
	go consumeStage(cfg, tracker, processor, tasks.StageChunk, cfg.Topics.Chunk, cfg.ConsumerGroupPrefix+"-chunk")
	go consumeStage(cfg, tracker, processor, tasks.StageEmbed, cfg.Topics.Embed, cfg.ConsumerGroupPrefix+"-embed")
	go consumeStage(cfg, tracker, processor, tasks.StageIndex, cfg.Topics.Index, cfg.ConsumerGroupPrefix+"-index")
}

// consumeStage handles consume stage.
func consumeStage(cfg serverconfig.KafkaConfig, tracker repository.PipelineTaskRepository, processor TaskProcessor, stage tasks.Stage, topic, groupID string) {
	brokers := parseKafkaBrokers(cfg.Brokers)
	dialer := &kafka.Dialer{
		Timeout:   10 * time.Second,
		DualStack: true,
		KeepAlive: 30 * time.Second,
	}

	readerConfig := pipelineReaderConfig(brokers, topic, groupID)
	readerConfig.Dialer = dialer
	r := kafka.NewReader(readerConfig)
	defer func() {
		if err := r.Close(); err != nil {
			log.Errorf("关闭 Kafka 消费者失败, stage=%s err=%v", stage, err)
		}
	}()

	log.Infof("Kafka 消费者启动, stage=%s topic=%s group=%s", stage, topic, groupID)

	for {
		m, err := r.FetchMessage(context.Background())
		if err != nil {
			log.Errorf("从 Kafka 读取消息失败, stage=%s err=%v", stage, err)
			time.Sleep(2 * time.Second)
			continue
		}

		var task tasks.FileProcessingTask
		if err := json.Unmarshal(m.Value, &task); err != nil {
			log.Errorf("无法解析 Kafka 消息, stage=%s offset=%d err=%v", stage, m.Offset, err)
			_ = r.CommitMessages(context.Background(), m)
			continue
		}
		if task.Stage == "" {
			task.Stage = stage
		}

		// 文件级任务：chunk_id 固定为 -1。后续可扩展到 chunk 级别。
		chunkID := -1
		if task.TaskChunkID > 0 {
			chunkID = task.TaskChunkID
		}
		// Run-aware dedup: a message carrying a RunID belongs to a controlled
		// replay and must NOT be skipped by a stale SUCCESS from a prior run.
		// Dedup within the same run is handled by MarkProcessingRun's upsert.
		// Legacy messages (no RunID) keep their original key and SUCCESS-skip
		// semantics, so existing pipeline_task rows behave exactly as before.
		hasRunID := strings.TrimSpace(task.RunID) != ""
		var previous *model.PipelineTask
		var getErr error
		if hasRunID {
			previous, getErr = tracker.GetByRunKey(task.RunID, task.FileMD5, string(task.Stage), chunkID)
		} else {
			previous, getErr = tracker.GetByKey(task.FileMD5, string(task.Stage), chunkID)
		}
		if getErr == nil && shouldSkipByStatus(previous, hasRunID) {
			_ = r.CommitMessages(context.Background(), m)
			continue
		}
		if getErr != nil && !errors.Is(getErr, gorm.ErrRecordNotFound) {
			log.Errorf("读取任务状态失败, stage=%s file=%s err=%v", stage, task.FileMD5, getErr)
			time.Sleep(time.Second)
			continue
		}

		if hasRunID {
			if _, err := tracker.MarkProcessingRun(task.RunID, task.FileMD5, string(task.Stage), chunkID); err != nil {
				log.Errorf("标记任务处理中失败, stage=%s file=%s run=%s err=%v", stage, task.FileMD5, task.RunID, err)
				time.Sleep(time.Second)
				continue
			}
		} else {
			if _, err := tracker.MarkProcessing(task.FileMD5, string(task.Stage), chunkID); err != nil {
				log.Errorf("标记任务处理中失败, stage=%s file=%s err=%v", stage, task.FileMD5, err)
				time.Sleep(time.Second)
				continue
			}
		}

		if err := processor.Process(context.Background(), task); err != nil {
			var retryCount int
			var markErr error
			if hasRunID {
				retryCount, markErr = tracker.MarkRetryRun(task.RunID, task.FileMD5, string(task.Stage), chunkID, err.Error())
			} else {
				retryCount, markErr = tracker.MarkRetry(task.FileMD5, string(task.Stage), chunkID, err.Error())
			}
			if markErr != nil {
				log.Errorf("标记任务重试失败, stage=%s file=%s err=%v", stage, task.FileMD5, markErr)
			}
			if retryCount <= cfg.MaxRetries {
				backoff := time.Duration(cfg.BaseBackoffMs) * time.Millisecond * time.Duration(1<<(retryCount-1))
				log.Warnf("任务处理失败, stage=%s file=%s retry=%d/%d backoff=%s err=%v", stage, task.FileMD5, retryCount, cfg.MaxRetries, backoff, err)
				time.Sleep(backoff)
				task.LastError = err.Error()
				if produceErr := produceToTopic(context.Background(), topic, task); produceErr != nil {
					log.Errorf("重投 Kafka 失败, stage=%s file=%s err=%v", stage, task.FileMD5, produceErr)
				}
			} else {
				task.LastError = err.Error()
				if hasRunID {
					_ = tracker.MarkFailedRun(task.RunID, task.FileMD5, string(task.Stage), chunkID, err.Error())
				} else {
					_ = tracker.MarkFailed(task.FileMD5, string(task.Stage), chunkID, err.Error())
				}
				if dlqErr := ProduceTaskToDLQ(task); dlqErr != nil {
					log.Errorf("写入 DLQ 失败, stage=%s file=%s err=%v", stage, task.FileMD5, dlqErr)
				} else {
					log.Errorf("任务进入 DLQ, stage=%s file=%s", stage, task.FileMD5)
				}
			}
			_ = r.CommitMessages(context.Background(), m)
			continue
		}

		if hasRunID {
			if err := tracker.MarkSuccessRun(task.RunID, task.FileMD5, string(task.Stage), chunkID); err != nil {
				log.Errorf("标记任务成功失败, stage=%s file=%s run=%s err=%v", stage, task.FileMD5, task.RunID, err)
			}
		} else {
			if err := tracker.MarkSuccess(task.FileMD5, string(task.Stage), chunkID); err != nil {
				log.Errorf("标记任务成功失败, stage=%s file=%s err=%v", stage, task.FileMD5, err)
			}
		}
		if err := r.CommitMessages(context.Background(), m); err != nil {
			log.Errorf("提交 Kafka offset 失败, stage=%s offset=%d err=%v", stage, m.Offset, err)
		}
	}
}

func pipelineReaderConfig(brokers []string, topic, groupID string) kafka.ReaderConfig {
	return kafka.ReaderConfig{
		Brokers:          brokers,
		Topic:            topic,
		GroupID:          groupID,
		MinBytes:         10e3,
		MaxBytes:         10e6,
		MaxWait:          time.Second,
		ReadBatchTimeout: 5 * time.Second,
	}
}

// shouldSkipByStatus decides whether a fetched Kafka message should be skipped
// because the prior pipeline_task row for its dedup key is already SUCCESS.
//
// Legacy semantics (hasRunID==false): skip iff a prior SUCCESS row exists —
// identical to the pre-run-aware behavior, so the 44 existing legacy rows and
// ordinary uploads are unaffected.
//
// Run semantics (hasRunID==true): NEVER skip on a prior SUCCESS. A controlled
// replay (distinct RunID) must always execute end-to-end; intra-run duplicate
// messages are deduped by MarkProcessingRun's idempotent upsert, not by the
// SUCCESS check here. A nil previous (no row yet) never skips.
func shouldSkipByStatus(previous *model.PipelineTask, hasRunID bool) bool {
	if previous == nil {
		return false
	}
	if hasRunID {
		return false
	}
	return previous.Status == model.PipelineStatusSuccess
}

// parseKafkaBrokers handles parse kafka brokers.
func parseKafkaBrokers(raw string) []string {
	parts := strings.Split(raw, ",")
	brokers := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			brokers = append(brokers, part)
		}
	}
	if len(brokers) == 0 {
		return []string{"127.0.0.1:9092"}
	}
	return brokers
}

// isRetriableProduceError reports whether retriable produce error.
func isRetriableProduceError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "unknown topic or partition") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "leader not available") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "unexpected eof")
}

// maxInt returns the larger of two integers.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
