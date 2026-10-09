// Package serverconfig loads and manages the RAG server configuration.
package serverconfig

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/spf13/viper"
)

// 全局配置变量，存储从配置文件加载的所有设置。
var Conf Config

// Config 是整个应用程序的配置结构体，与 config.yaml 文件结构对应。
type Config struct {
	Server        ServerConfig        `mapstructure:"server"`
	Harness       HarnessConfig       `mapstructure:"harness"`
	Database      DatabaseConfig      `mapstructure:"database"`
	JWT           JWTConfig           `mapstructure:"jwt"`
	Log           LogConfig           `mapstructure:"log"`
	Kafka         KafkaConfig         `mapstructure:"kafka"`
	Tika          TikaConfig          `mapstructure:"tika"`
	Elasticsearch ElasticsearchConfig `mapstructure:"elasticsearch"`
	MinIO         MinIOConfig         `mapstructure:"minio"`
	Embedding     EmbeddingConfig     `mapstructure:"embedding"`
	LLM           LLMConfig           `mapstructure:"llm"`
	Retrieval     RetrievalConfig     `mapstructure:"retrieval"`
	Reranker      RerankerConfig      `mapstructure:"reranker"`
	Memory        MemoryConfig        `mapstructure:"memory"`
	AI            AIConfig            `mapstructure:"ai"`
	Corpus        CorpusConfig        `mapstructure:"corpus"`
}

// HarnessConfig contains local durable runtime paths. The ledger is the sole
// writable source for browser sessions and is deliberately independent of the
// legacy GORM snapshot tables.
type HarnessConfig struct {
	SessionLedgerPath string `mapstructure:"session_ledger_path"`
	IdentityPath      string `mapstructure:"identity_path"`
}

func DefaultHarnessConfig() HarnessConfig {
	return HarnessConfig{SessionLedgerPath: ".agent/sessions/sessions.sqlite"}
}

// CorpusConfig controls versioned corpus writes without switching read aliases by default.
type CorpusConfig struct {
	Generation        string `mapstructure:"generation"`
	TextIndex         string `mapstructure:"text_index"`
	ReadAlias         string `mapstructure:"read_alias"`
	VisualPilotPrefix string `mapstructure:"visual_pilot_prefix"`
	VisualAlias       string `mapstructure:"visual_alias"`
	LoaderUser        uint   `mapstructure:"loader_user"`
	AllowAliasSwitch  bool   `mapstructure:"allow_alias_switch"`
}

// DefaultCorpusConfig returns the safe, non-cutover corpus configuration.
func DefaultCorpusConfig() CorpusConfig {
	return CorpusConfig{
		Generation:        "techdocs-2026-07-30-v1",
		TextIndex:         "knowledge_base_v2_bge_m3",
		ReadAlias:         "knowledge_base_current",
		VisualPilotPrefix: "knowledge_page_visual_pilot",
		VisualAlias:       "knowledge_page_visual_current",
		LoaderUser:        1,
		AllowAliasSwitch:  false,
	}
}

// ServerConfig 存储服务器相关的配置。
type ServerConfig struct {
	Profile        string `mapstructure:"profile"`
	Host           string `mapstructure:"host"`
	FrontendDir    string `mapstructure:"frontend_dir"`
	Port           string `mapstructure:"port"`
	Mode           string `mapstructure:"mode"`
	AllowedOrigins string `mapstructure:"allowed_origins"`
}

// DatabaseConfig 存储所有数据库连接的配置。
type DatabaseConfig struct {
	MySQL MySQLConfig `mapstructure:"mysql"`
	Redis RedisConfig `mapstructure:"redis"`
}

// MySQLConfig 存储 MySQL 数据库的配置。
type MySQLConfig struct {
	DSN string `mapstructure:"dsn"`
}

// RedisConfig 存储 Redis 的配置。
type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// JWTConfig 存储 JWT 相关的配置。
type JWTConfig struct {
	Secret                 string `mapstructure:"secret"`
	AccessTokenExpireHours int    `mapstructure:"access_token_expire_hours"`
	RefreshTokenExpireDays int    `mapstructure:"refresh_token_expire_days"`
}

// Validate rejects configurations that would silently start with an insecure
// or non-durable runtime. It is called before any external client is opened.
func (c Config) Validate() error {
	invalid := make([]string, 0, 7)
	if strings.TrimSpace(c.Server.Port) == "" {
		invalid = append(invalid, "server.port")
	}
	origins := strings.TrimSpace(c.Server.AllowedOrigins)
	if origins == "" {
		invalid = append(invalid, "server.allowed_origins")
	} else if origins == "*" || strings.Contains(origins, "*") {
		invalid = append(invalid, "server.allowed_origins (wildcard is forbidden)")
	}
	if strings.TrimSpace(c.Harness.SessionLedgerPath) == "" {
		invalid = append(invalid, "harness.session_ledger_path")
	}
	if c.Server.Profile == "local-core" {
		if host := c.Server.Host; host != "" {
			if address := net.ParseIP(host); address == nil || !address.IsLoopback() {
				invalid = append(invalid, "server.host (local core requires loopback)")
			}
		}
		if strings.TrimSpace(c.Harness.IdentityPath) == "" {
			invalid = append(invalid, "harness.identity_path")
		}
		identity, identityErr := filepath.Abs(c.Harness.IdentityPath)
		ledger, ledgerErr := filepath.Abs(c.Harness.SessionLedgerPath)
		if identityErr != nil || ledgerErr != nil || strings.EqualFold(identity, ledger) {
			invalid = append(invalid, "identity and Session Ledger paths must be distinct")
		}
	} else {
		if c.Server.Profile != "" && c.Server.Profile != "full-stack" {
			invalid = append(invalid, "server.profile")
		}
		if strings.TrimSpace(c.Database.MySQL.DSN) == "" {
			invalid = append(invalid, "database.mysql.dsn")
		}
		if strings.TrimSpace(c.Database.Redis.Addr) == "" {
			invalid = append(invalid, "database.redis.addr")
		}
	}
	secret := strings.TrimSpace(c.JWT.Secret)
	if secret == "" {
		invalid = append(invalid, "jwt.secret")
	} else if secret == "dev-secret-key-change-in-production" || len(secret) < 32 {
		invalid = append(invalid, "jwt.secret (minimum 32 characters)")
	}
	if c.JWT.AccessTokenExpireHours <= 0 {
		invalid = append(invalid, "jwt.access_token_expire_hours")
	}
	if c.JWT.RefreshTokenExpireDays <= 0 {
		invalid = append(invalid, "jwt.refresh_token_expire_days")
	}
	if len(invalid) > 0 {
		return errors.New("invalid server configuration: missing or invalid " + strings.Join(invalid, ", "))
	}
	return nil
}

// LogConfig 存储日志相关的配置。
type LogConfig struct {
	Level      string `mapstructure:"level"`
	Format     string `mapstructure:"format"`
	OutputPath string `mapstructure:"output_path"`
}

// KafkaConfig 存储 Kafka 相关的配置。
type KafkaConfig struct {
	Brokers string `mapstructure:"brokers"`
	// ConsumersEnabled controls the optional corpus ingestion consumers. The
	// interactive Agent Harness does not require Kafka, so this is fail-closed
	// by default and must be enabled explicitly for ingestion deployments.
	ConsumersEnabled    bool              `mapstructure:"consumers_enabled"`
	Topic               string            `mapstructure:"topic"`
	Topics              KafkaTopicsConfig `mapstructure:"topics"`
	ConsumerGroupPrefix string            `mapstructure:"consumer_group_prefix"`
	MaxRetries          int               `mapstructure:"max_retries"`
	BaseBackoffMs       int               `mapstructure:"base_backoff_ms"`
	EmbeddingBatchSize  int               `mapstructure:"embedding_batch_size"`
	ESBulkBatchSize     int               `mapstructure:"es_bulk_batch_size"`
}

// KafkaTopicsConfig stores kafka topics configuration.
type KafkaTopicsConfig struct {
	Parse string `mapstructure:"parse"`
	Chunk string `mapstructure:"chunk"`
	Embed string `mapstructure:"embed"`
	Index string `mapstructure:"index"`
	DLQ   string `mapstructure:"dlq"`
}

// TikaConfig 存储 Tika 服务器相关的配置。
type TikaConfig struct {
	ServerURL string `mapstructure:"server_url"`
}

// ElasticsearchConfig 存储 Elasticsearch 相关的配置。
type ElasticsearchConfig struct {
	Addresses string `mapstructure:"addresses"`
	Username  string `mapstructure:"username"`
	Password  string `mapstructure:"password"`
	IndexName string `mapstructure:"index_name"`
}

// MinIOConfig 存储 MinIO 对象存储的配置。
type MinIOConfig struct {
	Endpoint        string `mapstructure:"endpoint"`
	AccessKeyID     string `mapstructure:"access_key_id"`
	SecretAccessKey string `mapstructure:"secret_access_key"`
	UseSSL          bool   `mapstructure:"use_ssl"`
	BucketName      string `mapstructure:"bucket_name"`
}

// EmbeddingConfig 存储 Embedding 模型相关的配置。
type EmbeddingConfig struct {
	APIKey                  string `mapstructure:"api_key"`
	BaseURL                 string `mapstructure:"base_url"`
	Model                   string `mapstructure:"model"`
	ModelRevision           string `mapstructure:"model_revision"`
	Dimensions              int    `mapstructure:"dimensions"`
	ExpectedDimensions      int    `mapstructure:"expected_dimensions"`
	HealthPath              string `mapstructure:"health_path"`
	RequireNativeDimensions bool   `mapstructure:"require_native_dimensions"`
}

// LLMConfig 存储大语言模型相关的配置。
type LLMConfig struct {
	APIKey     string              `mapstructure:"api_key"`
	BaseURL    string              `mapstructure:"base_url"`
	Model      string              `mapstructure:"model"`
	Generation LLMGenerationConfig `mapstructure:"generation"`
	Prompt     LLMPromptConfig     `mapstructure:"prompt"`
}

// LLMGenerationConfig 配置生成相关参数（可选）。
type LLMGenerationConfig struct {
	Temperature float64 `mapstructure:"temperature"`
	TopP        float64 `mapstructure:"top_p"`
	MaxTokens   int     `mapstructure:"max_tokens"`
}

// LLMPromptConfig 配置系统提示与上下文包裹格式（可选）。
type LLMPromptConfig struct {
	Rules        string `mapstructure:"rules"`
	RefStart     string `mapstructure:"ref_start"`
	RefEnd       string `mapstructure:"ref_end"`
	NoResultText string `mapstructure:"no_result_text"`
}

// RetrievalConfig stores retrieval configuration.
type RetrievalConfig struct {
	BM25TopN        int `mapstructure:"bm25_topn"`
	VectorTopN      int `mapstructure:"vector_topn"`
	RRFK            int `mapstructure:"rrf_k"`
	RerankTopN      int `mapstructure:"rerank_topn"`
	FinalTopK       int `mapstructure:"final_topk"`
	RerankTimeoutMs int `mapstructure:"rerank_timeout_ms"`
	// EvidenceTokenBudget caps the total tokens of same-parent neighbor
	// evidence loaded per retrieval turn (0 uses the expander's default).
	EvidenceTokenBudget int `mapstructure:"evidence_token_budget"`
	// StrictMode enables acceptance-mode behavior: a vector dimension
	// mismatch must fail the request explicitly instead of silently
	// degrading to BM25 (design spec §10: no legacy fallback on mismatch).
	StrictMode bool `mapstructure:"strict_mode"`
}

// RerankerConfig stores reranker configuration.
type RerankerConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"`
}

// MemoryConfig stores memory configuration.
type MemoryConfig struct {
	Enabled                bool    `mapstructure:"enabled"`
	MemoryIndexName        string  `mapstructure:"memory_index_name"`
	SensoryMaxMessages     int     `mapstructure:"sensory_max_messages"`
	SensoryMaxTokens       int     `mapstructure:"sensory_max_tokens"`
	WorkingTriggerMessages int     `mapstructure:"working_trigger_messages"`
	WorkingMaxFacts        int     `mapstructure:"working_max_facts"`
	WorkingHistoryMessages int     `mapstructure:"working_history_messages"`
	ProfileMaxSlots        int     `mapstructure:"profile_max_slots"`
	LongTermTopK           int     `mapstructure:"long_term_topk"`
	ContextTopK            int     `mapstructure:"context_topk"`
	LongTermMinImportance  float64 `mapstructure:"long_term_min_importance"`
}

// AIConfig 对齐 Java 的 ai.prompt/ai.generation（连字符键）
type AIConfig struct {
	Orchestrator AIOrchestratorConfig `mapstructure:"orchestrator"`
	Generation   AIGenerationConfig   `mapstructure:"generation"`
	Prompt       AIPromptConfig       `mapstructure:"prompt"`
}

// AIOrchestratorConfig stores the external LangGraph service integration settings.
type AIOrchestratorConfig struct {
	Enabled            bool   `mapstructure:"enabled"`
	IngestionEnabled   bool   `mapstructure:"ingestion_enabled"`
	BaseURL            string `mapstructure:"base_url"`
	TimeoutMs          int    `mapstructure:"timeout_ms"`
	IngestionTimeoutMs int    `mapstructure:"ingestion_timeout_ms"`
	SharedSecret       string `mapstructure:"shared_secret"`
}

// AIGenerationConfig stores ai generation configuration.
type AIGenerationConfig struct {
	Temperature float64 `mapstructure:"temperature"`
	TopP        float64 `mapstructure:"top-p"`
	MaxTokens   int     `mapstructure:"max-tokens"`
}

// AIPromptConfig stores ai prompt configuration.
type AIPromptConfig struct {
	Rules        string `mapstructure:"rules"`
	RefStart     string `mapstructure:"ref-start"`
	RefEnd       string `mapstructure:"ref-end"`
	NoResultText string `mapstructure:"no-result-text"`
}

// Init 初始化配置加载，从指定的路径读取 YAML 文件并解析到 Conf 变量中。
func Init(configPath string) {
	viper.SetConfigFile(configPath)
	Conf = Config{Corpus: DefaultCorpusConfig(), Harness: DefaultHarnessConfig()}
	viper.SetConfigType("yaml")

	// Support ${ENV_VAR:default} expansion in config values so secrets
	// can stay out of the tracked config file.
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		panic(fmt.Errorf("读取配置文件失败: %w", err))
	}

	if err := viper.Unmarshal(&Conf); err != nil {
		panic(fmt.Errorf("无法将配置解析到结构体中: %w", err))
	}

	// Expand ${ENV:default} placeholders that viper doesn't natively support.
	expandEnvBind(&Conf)
	if value := strings.TrimSpace(os.Getenv("CODE_AGENT_SESSION_LEDGER_PATH")); value != "" {
		Conf.Harness.SessionLedgerPath = value
	}
}

// expandEnvBind walks a struct pointer with reflection and replaces
// "${ENV_VAR:default}" values using os.Getenv. Only string fields are
// touched; the default after the colon is used when the env var is unset
// or empty.
func expandEnvBind(v any) {
	val := reflect.ValueOf(v).Elem()
	for i := range val.NumField() {
		f := val.Field(i)
		if f.Kind() == reflect.String && f.CanSet() {
			s := f.String()
			if strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}") {
				inner := s[2 : len(s)-1]
				if idx := strings.IndexByte(inner, ':'); idx >= 0 {
					envKey := inner[:idx]
					def := inner[idx+1:]
					if envVal := os.Getenv(envKey); envVal != "" {
						f.SetString(envVal)
					} else {
						f.SetString(def)
					}
				}
			}
		}
		if f.Kind() == reflect.Struct {
			expandEnvBind(f.Addr().Interface())
		}
	}
}
