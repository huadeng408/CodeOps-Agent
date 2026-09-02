package kafka

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

func TestLiveReaderCanFetchParseBacklog(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_KAFKA_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_KAFKA_E2E=1 to run the Kafka reader integration")
	}
	brokers := strings.TrimSpace(os.Getenv("CODE_AGENT_KAFKA_BROKERS"))
	topic := strings.TrimSpace(os.Getenv("CODE_AGENT_KAFKA_PARSE_TOPIC"))
	groupID := strings.TrimSpace(os.Getenv("CODE_AGENT_KAFKA_PARSE_GROUP"))
	if brokers == "" || topic == "" || groupID == "" {
		t.Skip("set CODE_AGENT_KAFKA_BROKERS, CODE_AGENT_KAFKA_PARSE_TOPIC and CODE_AGENT_KAFKA_PARSE_GROUP")
	}
	config := pipelineReaderConfig(parseKafkaBrokers(brokers), topic, groupID)
	config.ErrorLogger = kafkago.LoggerFunc(t.Logf)
	r := kafkago.NewReader(config)
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	message, err := r.FetchMessage(ctx)
	if err != nil {
		t.Fatalf("FetchMessage: %v", err)
	}
	if len(message.Value) == 0 {
		t.Fatal("fetched empty Kafka message")
	}
}

func TestPipelineReaderTimeoutExceedsBrokerMaxWait(t *testing.T) {
	config := pipelineReaderConfig([]string{"test-broker"}, "test-topic", "test-group")
	if config.MaxWait <= 0 {
		t.Fatal("MaxWait must be explicit for low-volume pipeline topics")
	}
	if config.ReadBatchTimeout <= config.MaxWait {
		t.Fatalf("ReadBatchTimeout=%s must exceed MaxWait=%s", config.ReadBatchTimeout, config.MaxWait)
	}
}
