// Package main contains executable entrypoints.
package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"code-agent/internal/handler"
	"code-agent/internal/identity"
	"code-agent/internal/middleware"
	"code-agent/internal/model"
	harnessorch "code-agent/internal/orchestrator"
	"code-agent/internal/permission"
	"code-agent/internal/pipeline"
	"code-agent/internal/repository"
	"code-agent/internal/serverconfig"
	"code-agent/internal/service"
	"code-agent/internal/session"
	"code-agent/internal/telemetry/genai"
	"code-agent/internal/tools"
	"code-agent/internal/worktree"
	"code-agent/pkg/database"
	"code-agent/pkg/documentparser"
	"code-agent/pkg/embedding"
	"code-agent/pkg/es"
	"code-agent/pkg/kafka"
	"code-agent/pkg/log"
	"code-agent/pkg/mineru"
	"code-agent/pkg/orchestrator"
	"code-agent/pkg/reranker"
	"code-agent/pkg/storage"
	"code-agent/pkg/tika"
	"code-agent/pkg/token"

	"github.com/gin-gonic/gin"
)

// managedContinuation owns the paired runner and gRPC client so replacing the
// continuation slot also closes the transport without leaking resources.
type managedContinuation struct {
	runner session.ContinuationModule
	client *harnessorch.Client
}

func (m *managedContinuation) RequestContinuation(ctx context.Context, cmd session.ContinueCommand) (session.RunView, error) {
	if m == nil || m.runner == nil {
		return session.RunView{}, session.ErrContinuationUnavailable
	}
	return m.runner.RequestContinuation(ctx, cmd)
}

func (m *managedContinuation) Recover(ctx context.Context) error {
	if m == nil || m.runner == nil {
		return session.ErrContinuationUnavailable
	}
	return m.runner.Recover(ctx)
}

func (m *managedContinuation) Close() error {
	if m == nil {
		return nil
	}
	var firstErr error
	if m.runner != nil {
		firstErr = m.runner.Close()
	}
	if m.client != nil {
		if err := m.client.Close(); firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m *managedContinuation) Health(ctx context.Context) error {
	if m == nil || m.client == nil {
		return session.ErrContinuationUnavailable
	}
	response, err := m.client.Health(ctx)
	if err != nil {
		return err
	}
	if response == nil || !strings.EqualFold(strings.TrimSpace(response.Status), "ok") {
		return fmt.Errorf("orchestrator health is not ok")
	}
	return nil
}

// main bootstraps infrastructure dependencies and starts the HTTP server.
func main() {
	configPath := strings.TrimSpace(os.Getenv("CODEAGENT_CONFIG"))
	if configPath == "" {
		configPath = "./configs/server.yaml"
	}
	serverconfig.Init(configPath)
	cfg := serverconfig.Conf
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "server configuration rejected: %v\n", err)
		return
	}

	log.Init(cfg.Log.Level, cfg.Log.Format, cfg.Log.OutputPath)
	defer log.Sync()

	// The LangGraph orchestrator (chat/memory/ingestion) is optional in
	// localcode: the KB CRUD/search/upload APIs work without it. When it is
	// disabled or unreachable, chat endpoints return an error but the server
	// still starts. Set ai.orchestrator.enabled=true + base_url to enable.
	if !cfg.AI.Orchestrator.Enabled || strings.TrimSpace(cfg.AI.Orchestrator.BaseURL) == "" {
		log.Info("LangGraph orchestrator disabled — chat/memory endpoints will be unavailable; KB APIs still serve")
	} else if strings.TrimSpace(cfg.AI.Orchestrator.SharedSecret) == "" {
		log.Errorf("LangGraph orchestrator enabled but shared_secret is empty; disabling orchestrator client")
		cfg.AI.Orchestrator.Enabled = false
	}

	database.InitMySQL(cfg.Database.MySQL.DSN)
	database.InitRedis(cfg.Database.Redis.Addr, cfg.Database.Redis.Password, cfg.Database.Redis.DB)
	// AutoMigrate first so every table exists before EnsureRuntimeSchema
	// runs column-level ALTERs. localcode's docker-compose mounts no DDL,
	// so all tables must be created here (paismart relied on docs/ddl.sql).
	if err := database.DB.AutoMigrate(
		&model.User{},
		&model.OrganizationTag{},
		&model.Conversation{},
		&model.FileUpload{},
		&model.ChunkInfo{},
		&model.DocumentVector{},
		&model.KnowledgeSource{},
		&model.KnowledgeDocument{},
		&model.PipelineTask{},
		&model.WorkingMemorySnapshot{},
		&model.UserProfileSlot{},
		&model.LongTermMemory{},
	); err != nil {
		log.Errorf("failed to migrate runtime tables: %v", err)
		return
	}
	if err := database.EnsureRuntimeSchema(); err != nil {
		log.Errorf("failed to apply runtime schema migration: %v", err)
		return
	}

	storage.InitMinIO(cfg.MinIO)
	// ES is non-fatal: search/memory endpoints degrade without it, but the
	// server still serves user/auth/document APIs. Start ES separately to
	// enable search.
	if err := es.InitES(cfg.Elasticsearch, cfg.Embedding.Dimensions); err != nil {
		log.Warnf("elasticsearch unavailable — search endpoints degraded: %v", err)
	} else {
		if cfg.Memory.Enabled {
			if err := es.EnsureMemoryIndex(cfg.Memory.MemoryIndexName, cfg.Embedding.Dimensions); err != nil {
				log.Warnf("failed to init memory index — memory endpoints degraded: %v", err)
			}
		}
		// Ensure the corpus v2 physical index uses the correct mapping
		// (dense_vector, provenance keyword fields, section_path)
		// before any document lands on it via ES dynamic mapping.
		if es.ESClient != nil {
			mgr := es.NewKnowledgeIndexManager(es.ESClient, cfg.Elasticsearch.Addresses)
			if err := mgr.EnsurePhysicalIndex(context.Background(), cfg.Corpus.TextIndex, cfg.Embedding.Dimensions); err != nil {
				log.Warnf("corpus v2 index ensure failed — text indexing degraded: %v", err)
			}
		}
	}
	kafka.InitProducer(cfg.Kafka)

	// Embedding preflight is non-fatal: a failed check degrades retrieval but
	// must not block startup (same semantics as ES being unavailable). The
	// result is exposed via /healthz for ops/automation to judge.
	embeddingPreflightStatus := func() string {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := embedding.Preflight(ctx, cfg.Embedding); err != nil {
			log.Warnf("embedding preflight degraded (non-blocking): %v", err)
			return "degraded: embedding preflight failed"
		}
		log.Info("embedding preflight passed")
		return "ok"
	}()

	userRepository := repository.NewUserRepository(database.DB)
	orgTagRepo := repository.NewOrgTagRepository(database.DB)
	uploadRepo := repository.NewUploadRepository(database.DB, database.RDB)
	conversationRepo := repository.NewConversationRepository(database.RDB)
	docVectorRepo := repository.NewDocumentVectorRepository(database.DB)
	pipelineTaskRepo := repository.NewPipelineTaskRepository(database.DB)
	memoryRepo := repository.NewMemoryRepository(database.DB)

	jwtManager := token.NewJWTManager(cfg.JWT.Secret, cfg.JWT.AccessTokenExpireHours, cfg.JWT.RefreshTokenExpireDays)
	tikaClient := tika.NewClient(cfg.Tika)
	documentParser := documentparser.New(tikaClient, mineru.NewClient())
	embeddingClient := embedding.NewClient(cfg.Embedding)
	orchestratorClient := orchestrator.NewClient(cfg.AI.Orchestrator)
	orchestratorMemoryClient := orchestrator.NewMemoryClient(cfg.AI.Orchestrator)
	ingestionClient := orchestrator.NewIngestionClient(cfg.AI.Orchestrator)
	rerankerClient := reranker.NewClient(cfg.Reranker)

	userService := service.NewUserService(userRepository, orgTagRepo, jwtManager)
	documentRepo := repository.NewKnowledgeDocumentRepository(database.DB)
	adminService := service.NewAdminService(orgTagRepo, userRepository, conversationRepo, pipelineTaskRepo, uploadRepo, documentRepo, nil)
	uploadService := service.NewUploadService(uploadRepo, userRepository, cfg.MinIO)
	documentService := service.NewDocumentService(uploadRepo, userRepository, orgTagRepo, docVectorRepo, pipelineTaskRepo, cfg.MinIO, cfg.Elasticsearch.IndexName, documentParser)
	searchIndex, err := searchReadIndex(cfg)
	if err != nil {
		log.Errorf("search service configuration invalid: %v", err)
		return
	}
	strictTracePins := strings.EqualFold(strings.TrimSpace(os.Getenv("CODE_AGENT_STRICT_TRACE_PINS")), "true")
	traceIndex, err := resolveTraceIndexForStartup(
		context.Background(),
		strictTracePins,
		searchIndex,
		cfg.Corpus.TextIndex,
		func(ctx context.Context, alias string) ([]string, error) {
			if es.ESClient == nil {
				return nil, fmt.Errorf("elasticsearch client is unavailable")
			}
			return es.NewKnowledgeIndexManager(es.ESClient, cfg.Elasticsearch.Addresses).ReadAlias(ctx, alias)
		},
	)
	if err != nil {
		log.Errorf("search trace provenance invalid in strict mode: %v", err)
		return
	}
	searchService := service.NewSearchService(
		embeddingClient,
		rerankerClient,
		es.ESClient,
		userService,
		uploadRepo,
		searchIndex,
		cfg.Retrieval,
		cfg.Embedding.Model,
		cfg.Embedding.Dimensions,
	)
	conversationService := service.NewConversationService(conversationRepo)
	memoryService := service.NewMemoryService(memoryRepo, embeddingClient, orchestratorMemoryClient, rerankerClient, es.ESClient, cfg.Memory)
	orchestratorSupportService := service.NewOrchestratorSupportService(searchService, memoryService, conversationRepo, rerankerClient, docVectorRepo, userService)
	chatService := service.NewChatService(searchService, memoryService, conversationRepo, orchestratorClient, docVectorRepo, userService)
	// WebSocket Hub
	wsHub := handler.NewWebSocketHub()
	ledgerPath := strings.TrimSpace(cfg.Harness.SessionLedgerPath)
	if ledgerPath == "" {
		ledgerPath = filepath.Join(".agent", "sessions", "sessions.sqlite")
	}
	ledger, err := session.OpenSQLiteEventLog(ledgerPath)
	if err != nil {
		log.Errorf("failed to open canonical session ledger: %v", err)
		return
	}
	defer ledger.Close()
	workbench := session.NewWorkbench(ledger, wsHub)
	// Workspace recovery is read-only and scoped to active agent worktrees.
	// The manager is intentionally separate from the session ledger; it only
	// supplies git status for leases already bound to a session.
	workspaceManager := worktree.NewManager(".", "HEAD")
	// Browser continuations use the Go-owned SessionRunner. The gRPC client is
	// connection-only; actor, run identity, history, and callbacks are supplied
	// per request so concurrent Sessions cannot cross-write one another.
	continuationTarget := strings.TrimSpace(os.Getenv("CODE_AGENT_ORCHESTRATOR_ADDR"))
	if continuationTarget == "" {
		continuationTarget = "127.0.0.1:50051"
	}
	continuationRoot, rootErr := os.Getwd()
	if rootErr != nil || strings.TrimSpace(continuationRoot) == "" {
		continuationRoot = "."
	}
	continuationExecutor := tools.NewExecutor(continuationRoot)
	if err := continuationExecutor.SetWorkingDir(continuationRoot); err != nil {
		log.Warnf("session continuation working directory unavailable: %v", err)
	}
	continuationPermissions := continuationPermissionController()
	continuationTools := session.ToolExecutionFunc(func(ctx context.Context, actor identity.Actor, sessionID string, call harnessorch.ToolCall) harnessorch.ToolResult {
		var arguments map[string]any
		if strings.TrimSpace(call.ParametersJSON) != "" {
			if err := json.Unmarshal([]byte(call.ParametersJSON), &arguments); err != nil {
				return harnessorch.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "invalid tool parameters", ExitCode: 1}
			}
		}
		if arguments == nil {
			arguments = map[string]any{}
		}
		if continuationPermissions.CheckFor(actor, call.Name, arguments) != permission.Approve {
			return harnessorch.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "tool permission not granted", ExitCode: 1}
		}
		result, err := continuationExecutor.Execute(ctx, tools.ToolRequest{
			Name: call.Name, Arguments: arguments, OwnerSessionID: sessionID,
		})
		out := harnessorch.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Output: result.Output, Error: result.Error, ExitCode: int32(result.ExitCode), Truncated: result.Truncated}
		if err != nil && out.Error == "" {
			out.Error = err.Error()
		}
		if err != nil && out.ExitCode == 0 {
			out.ExitCode = 1
		}
		return out
	})
	continuationSlot := session.NewContinuationSlot()
	workerID := "server:" + strings.TrimSpace(cfg.Server.Port)
	connectContinuation := func(ctx context.Context) (session.ContinuationModule, error) {
		client, err := harnessorch.NewClient(continuationTarget)
		if err != nil {
			return nil, err
		}
		runner := session.NewSessionRunner(workbench, client, continuationTools, session.SessionRunnerOptions{WorkerID: workerID})
		return &managedContinuation{runner: runner, client: client}, nil
	}
	continuationSupervisor := session.NewContinuationSupervisor(continuationSlot, 5*time.Second, connectContinuation)
	continuationSupervisor.Start(context.Background())
	defer continuationSupervisor.Close()
	defer continuationExecutor.Close()
	wsTickets := session.NewWebSocketTickets(30 * time.Second)

	// Telemetry: create tracer and wire into handlers and services.
	telemetry := genai.NewTelemetry(context.Background())
	searchService.SetTracer(telemetry)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = telemetry.Shutdown(shutdownCtx)
	}()

	processor := pipeline.NewProcessor(
		documentParser,
		embeddingClient,
		cfg.Elasticsearch,
		cfg.MinIO,
		cfg.Embedding,
		cfg.Corpus,
		cfg.Kafka,
		uploadRepo,
		docVectorRepo,
		ingestionClient,
		documentRepo,
	)
	go kafka.StartPipelineConsumers(cfg.Kafka, processor, pipelineTaskRepo)

	initCtx, cancelInit := context.WithCancel(context.Background())
	defer cancelInit()
	go initSeedFiles(initCtx, "initfile", userRepository, uploadService)

	gin.SetMode(cfg.Server.Mode)
	r := gin.New()

	r.Use(func(c *gin.Context) {
		if len(c.Request.URL.Path) >= len("/api/") && c.Request.URL.Path[:len("/api/")] == "/api/" {
			if c.Writer.Header().Get("Content-Type") == "" {
				c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
			}
		}
		c.Next()
	})
	r.Use(corsMiddleware(cfg.Server.AllowedOrigins))
	r.Use(middleware.RequestLogger(), gin.Recovery())
	r.GET("/healthz", healthzHandlerWithContinuation(func() string { return embeddingPreflightStatus }, continuationSupervisor.Status))

	apiV1 := r.Group("/api/v1")
	{
		auth := apiV1.Group("/auth")
		{
			auth.POST("/refreshToken", handler.NewAuthHandler(userService).RefreshToken)
		}

		users := apiV1.Group("/users")
		{
			users.POST("/register", handler.NewUserHandler(userService).Register)
			users.POST("/login", handler.NewUserHandler(userService).Login)

			authed := users.Group("/")
			authed.Use(middleware.AuthMiddleware(jwtManager, userService))
			{
				authed.GET("/me", handler.NewUserHandler(userService).GetProfile)
				authed.POST("/logout", handler.NewUserHandler(userService).Logout)
				authed.PUT("/primary-org", handler.NewUserHandler(userService).SetPrimaryOrg)
				authed.GET("/org-tags", handler.NewUserHandler(userService).GetUserOrgTags)
			}
		}

		upload := apiV1.Group("/upload")
		upload.Use(middleware.AuthMiddleware(jwtManager, userService))
		{
			h := handler.NewUploadHandler(uploadService)
			upload.POST("/check", h.CheckFile)
			upload.POST("/chunk", h.UploadChunk)
			upload.POST("/merge", h.MergeChunks)
			upload.GET("/status", h.GetUploadStatus)
			upload.GET("/supported-types", h.GetSupportedFileTypes)
			upload.POST("/fast-upload", h.FastUpload)
		}

		documents := apiV1.Group("/documents")
		documents.Use(middleware.AuthMiddleware(jwtManager, userService))
		{
			dh := handler.NewDocumentHandler(documentService, userService)
			documents.GET("/accessible", dh.ListAccessibleFiles)
			documents.GET("/uploads", dh.ListUploadedFiles)
			documents.DELETE("/:fileMd5", dh.DeleteDocument)
			documents.GET("/download", dh.GenerateDownloadURL)
			documents.GET("/preview", dh.PreviewFile)
		}

		search := apiV1.Group("/search")
		search.Use(middleware.AuthMiddleware(jwtManager, userService))
		{
			searchHandler := handler.NewSearchHandler(searchService)
			searchHandler.SetTracer(telemetry)
			search.GET("/hybrid", searchHandler.HybridSearch)
		}

		conversation := apiV1.Group("/users/conversation")
		conversation.Use(middleware.AuthMiddleware(jwtManager, userService))
		{
			conversation.GET("", handler.NewConversationHandler(conversationService).GetConversations)
		}

		sessions := apiV1.Group("/sessions")
		sessions.Use(middleware.AuthMiddleware(jwtManager, userService))
		{
			sessionHandler := handler.NewSessionHandlerWithWorktree(workbench, wsTickets, workspaceManager)
			sessions.POST("", sessionHandler.Create)
			sessions.GET("", sessionHandler.List)
			sessions.GET("/:id", sessionHandler.Get)
			sessions.GET("/:id/runs", sessionHandler.RunHistory)
			sessions.GET("/:id/runs/:runId", sessionHandler.RunDetail)
			sessions.GET("/:id/recovery-manifest", sessionHandler.RecoveryManifest)
			sessions.GET("/:id/workspace-manifest", sessionHandler.WorkspaceManifest)
			sessions.PUT("/:id/title", sessionHandler.UpdateTitle)
			sessions.PUT("/:id/status", sessionHandler.UpdateStatus)
			sessions.DELETE("/:id", sessionHandler.Delete)
			sessions.POST("/:id/ws-ticket", sessionHandler.IssueWebSocketTicket)

			eventHandler := handler.NewEventHandlerWithContinuationSlot(workbench, continuationSlot)
			sessions.GET("/:id/events", eventHandler.ListEvents)
			sessions.POST("/:id/events", eventHandler.CreateEvent)
			sessions.GET("/:id/checkpoints", eventHandler.ListCheckpoints)
			sessions.POST("/:id/checkpoints", eventHandler.CreateCheckpoint)
			sessions.POST("/:id/restore/:hash", eventHandler.RestoreCheckpoint)
			sessions.POST("/:id/continue", eventHandler.ContinueSession)

		}
		// WebSocket uses an opaque one-time ticket, not an access JWT in the URL.
		wsHandler := handler.NewWebSocketHandlerWithWorkbench(wsHub, workbench, wsTickets)
		r.GET("/api/v1/sessions/:id/ws", wsHandler.HandleWebSocket)

		chatGroup := apiV1.Group("/chat")
		chatHandler := handler.NewChatHandler(chatService, userService, jwtManager)
		{
			chatGroup.GET("/websocket-token", chatHandler.GetWebsocketStopToken)
		}
		r.GET("/chat/:token", chatHandler.Handle)

		admin := apiV1.Group("/admin")
		admin.Use(middleware.AuthMiddleware(jwtManager, userService), middleware.AdminAuthMiddleware())
		{
			ah := handler.NewAdminHandler(adminService, userService)
			admin.GET("/users/list", ah.ListUsers)
			admin.PUT("/users/:userId/org-tags", ah.AssignOrgTagsToUser)
			admin.GET("/conversation", ah.GetAllConversations)
			admin.POST("/pipeline/replay", ah.ReplayPipelineTask)

			orgTags := admin.Group("/org-tags")
			{
				orgTags.POST("", ah.CreateOrganizationTag)
				orgTags.GET("", ah.ListOrganizationTags)
				orgTags.GET("/tree", ah.GetOrganizationTagTree)
				orgTags.PUT("/:id", ah.UpdateOrganizationTag)
				orgTags.DELETE("/:id", ah.DeleteOrganizationTag)
			}
		}

		internalGroup := r.Group("/internal")
		internalGroup.Use(middleware.TraceContextMiddleware(), middleware.InternalAuthMiddleware())
		{
			orchHandler := handler.NewOrchestratorHandler(orchestratorSupportService)
			orchHandler.SetTracer(telemetry)
			orchHandler.SetRAGTracePins(cfg.Corpus.Generation, traceIndex)
			corpusSourceRepo := repository.NewKnowledgeSourceRepository(database.DB)
			corpusDocRepo := repository.NewKnowledgeDocumentRepository(database.DB)
			corpusIngestService := service.NewCorpusIngestService(corpusSourceRepo, corpusDocRepo, cfg.Corpus, nil)
			knowledgeIngestHandler := handler.NewKnowledgeIngestHandler(corpusIngestService, cfg.MinIO)
			knowledgeDocumentHandler := handler.NewKnowledgeDocumentHandler(corpusDocRepo, cfg.Corpus)
			pipelineStatusHandler := handler.NewPipelineStatusHandler(pipelineTaskRepo)
			internalGroup.POST("/orchestrator/session", orchHandler.LoadSession)
			internalGroup.POST("/orchestrator/retrieve", orchHandler.RetrieveContext)
			internalGroup.POST("/orchestrator/prompt-context", orchHandler.PreparePromptContext)
			internalGroup.POST("/orchestrator/knowledge-search", orchHandler.SearchKnowledge)
			internalGroup.POST("/orchestrator/memory-search", orchHandler.SearchMemory)
			internalGroup.POST("/orchestrator/rerank-context", orchHandler.RerankContext)
			internalGroup.POST("/orchestrator/persist", orchHandler.PersistTurn)
			internalGroup.POST("/orchestrator/knowledge-ingest", knowledgeIngestHandler.Ingest)
			// Read-only document status query the importer polls after an
			// ingestion; InternalAuthMiddleware already guards the group.
			internalGroup.GET("/orchestrator/knowledge-documents", knowledgeDocumentHandler.List)
			internalGroup.GET("/orchestrator/pipeline-status", pipelineStatusHandler.Get)
		}
	}

	srv := &http.Server{Addr: fmt.Sprintf(":%s", cfg.Server.Port), Handler: r}

	go func() {
		log.Infof("server started on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("shutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("failed to shutdown server: %v", err)
	}
	log.Info("server stopped")
}

func continuationPermissionController() *permission.Controller {
	levels := make(map[string]permission.Level, len(permission.DefaultPermissions))
	for name, level := range permission.DefaultPermissions {
		levels[name] = level
	}
	// The server config intentionally has no browser-side approval channel.
	// Only AutoAllow tools can execute during a resumed run; mutating tools
	// remain fail-closed until an explicit Harness approval surface is wired.
	return permission.NewController(levels, nil)
}

func searchReadIndex(cfg serverconfig.Config) (string, error) {
	readAlias := strings.TrimSpace(cfg.Corpus.ReadAlias)
	if readAlias == "" {
		return "", fmt.Errorf("corpus.read_alias must be configured for search")
	}
	return readAlias, nil
}

type aliasResolver func(context.Context, string) ([]string, error)

// resolveSearchTraceIndex binds the configured read alias to the physical
// index that the live Elasticsearch cluster actually returns. O3 evidence may
// only claim a pin after this single-target, expected-index check succeeds.
func resolveSearchTraceIndex(ctx context.Context, readAlias, expectedIndex string, resolve aliasResolver) (string, error) {
	readAlias = strings.TrimSpace(readAlias)
	expectedIndex = strings.TrimSpace(expectedIndex)
	if readAlias == "" || expectedIndex == "" {
		return "", fmt.Errorf("read alias and expected physical index are required")
	}
	targets, err := resolve(ctx, readAlias)
	if err != nil {
		return "", fmt.Errorf("resolve read alias %q: %w", readAlias, err)
	}
	if len(targets) != 1 || strings.TrimSpace(targets[0]) != expectedIndex {
		return "", fmt.Errorf("read alias %q must resolve to exactly one expected physical index %q, got %v", readAlias, expectedIndex, targets)
	}
	return expectedIndex, nil
}

// resolveTraceIndexForStartup keeps the normal server's documented ES
// degradation behavior. O3 explicitly opts into strict pins and then must not
// start unless the live read alias resolves to the intended physical index.
func resolveTraceIndexForStartup(ctx context.Context, strict bool, readAlias, expectedIndex string, resolve aliasResolver) (string, error) {
	index, err := resolveSearchTraceIndex(ctx, readAlias, expectedIndex, resolve)
	if err != nil && !strict {
		return "", nil
	}
	return index, err
}

// healthzHandler serves the liveness endpoint, reporting the embedding
// preflight status (from statusFn) alongside the base ok status so
// ops/automation can judge whether retrieval is fully healthy.
func healthzHandler(statusFn func() string) gin.HandlerFunc {
	return healthzHandlerWithContinuation(statusFn, nil)
}

func healthzHandlerWithContinuation(statusFn func() string, continuationFn func() session.ContinuationSupervisorStatus) gin.HandlerFunc {
	return func(c *gin.Context) {
		body := gin.H{"status": "ok", "embedding_preflight": statusFn()}
		if continuationFn != nil {
			state := continuationFn()
			body["continuation"] = gin.H{
				"attached":           state.Attached,
				"generation":         state.Generation,
				"last_health_error":  state.LastHealthError,
				"last_recovery_at":   state.LastRecoveryAt,
				"last_transition_at": state.LastTransitionAt,
			}
		}
		c.JSON(http.StatusOK, body)
	}
}

func corsMiddleware(rawOrigins string) gin.HandlerFunc {
	allowed := make(map[string]struct{})
	for _, value := range strings.Split(rawOrigins, ",") {
		if origin := strings.TrimSpace(value); origin != "" {
			allowed[origin] = struct{}{}
		}
	}
	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin != "" {
			if _, ok := allowed[origin]; ok {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Header("Access-Control-Allow-Credentials", "true")
				c.Header("Vary", "Origin")
			}
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Refresh-Token")
		if c.Request.Method == http.MethodOptions {
			if origin != "" {
				if _, ok := allowed[origin]; !ok {
					c.AbortWithStatus(http.StatusForbidden)
					return
				}
			}
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// initSeedFiles imports local seed files through the normal upload pipeline on startup.
func initSeedFiles(ctx context.Context, dir string, userRepo repository.UserRepository, uploadSvc service.UploadService) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		log.Infof("initSeedFiles: directory '%s' not found, skipped", dir)
		return
	}

	var ownerUserID uint
	var ownerOrg string
	if admin, err := userRepo.FindByUsername("admin"); err == nil && admin != nil {
		ownerUserID = admin.ID
		ownerOrg = admin.PrimaryOrg
	} else {
		users, err := userRepo.FindAll()
		if err != nil || len(users) == 0 {
			log.Warnf("initSeedFiles: no available user, skipped")
			return
		}
		ownerUserID = users[0].ID
		ownerOrg = users[0].PrimaryOrg
	}

	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			log.Warnf("initSeedFiles: failed to open file: %s, err=%v", path, err)
			return nil
		}
		h := md5.New()
		size, copyErr := io.Copy(h, f)
		_ = f.Close()
		if copyErr != nil {
			log.Warnf("initSeedFiles: failed to read file: %s, err=%v", path, copyErr)
			return nil
		}
		fileMD5 := fmt.Sprintf("%x", h.Sum(nil))
		fileName := info.Name()

		if uploaded, ferr := uploadSvc.FastUpload(ctx, fileMD5, ownerUserID); ferr == nil && uploaded {
			log.Infof("initSeedFiles: skip existing file %s (md5=%s)", fileName, fileMD5)
			return nil
		}

		const chunkSize int64 = 5 * 1024 * 1024
		totalChunks := int(math.Ceil(float64(size) / float64(chunkSize)))
		if totalChunks == 0 {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			log.Warnf("initSeedFiles: failed to reopen file: %s, err=%v", path, err)
			return nil
		}
		defer file.Close()

		for chunkIndex := 0; chunkIndex < totalChunks; chunkIndex++ {
			offset := int64(chunkIndex) * chunkSize
			if _, err := file.Seek(offset, io.SeekStart); err != nil {
				log.Warnf("initSeedFiles: seek failed: %s, chunk=%d, err=%v", path, chunkIndex, err)
				return nil
			}
			toRead := chunkSize
			if offset+toRead > size {
				toRead = size - offset
			}
			buf := make([]byte, toRead)
			if _, err := io.ReadFull(file, buf); err != nil {
				log.Warnf("initSeedFiles: read chunk failed: %s, chunk=%d, err=%v", path, chunkIndex, err)
				return nil
			}
			chunkMD5 := fmt.Sprintf("%x", md5.Sum(buf))
			cf := &chunkFile{Reader: bytes.NewReader(buf)}
			if _, _, err := uploadSvc.UploadChunk(ctx, fileMD5, fileName, size, chunkIndex, cf, chunkMD5, ownerUserID, ownerOrg, true); err != nil {
				log.Warnf("initSeedFiles: upload chunk failed: %s, chunk=%d, err=%v", path, chunkIndex, err)
				return nil
			}
		}

		if _, err := uploadSvc.MergeChunks(ctx, fileMD5, fileName, ownerUserID); err != nil {
			log.Warnf("initSeedFiles: merge failed: %s, err=%v", path, err)
			return nil
		}
		log.Infof("initSeedFiles: imported and triggered pipeline: %s", fileName)
		return nil
	})
	if walkErr != nil {
		log.Warnf("initSeedFiles: walk directory error: %v", walkErr)
	}
}

// chunkFile wraps an in-memory reader so startup imports can reuse the chunk upload API.
type chunkFile struct{ Reader *bytes.Reader }

// Read proxies sequential reads to the underlying in-memory chunk buffer.
func (c *chunkFile) Read(p []byte) (int, error) { return c.Reader.Read(p) }

// ReadAt proxies random-access reads to the underlying in-memory chunk buffer.
func (c *chunkFile) ReadAt(p []byte, off int64) (int, error) { return c.Reader.ReadAt(p, off) }

// Seek repositions the in-memory chunk reader.
func (c *chunkFile) Seek(offset int64, whence int) (int64, error) {
	return c.Reader.Seek(offset, whence)
}

// Close satisfies the multipart.File contract for the in-memory chunk wrapper.
func (c *chunkFile) Close() error { return nil }
