// Package main contains executable entrypoints.
package main

import (
	"bytes"
	"context"
	"crypto/md5"
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

	"code-agent/internal/serverconfig"
	"code-agent/internal/handler"
	"code-agent/internal/middleware"
	"code-agent/internal/model"
	"code-agent/internal/pipeline"
	"code-agent/internal/repository"
	"code-agent/internal/service"
	"code-agent/pkg/database"
	"code-agent/pkg/embedding"
	"code-agent/pkg/es"
	"code-agent/pkg/kafka"
	"code-agent/pkg/log"
	"code-agent/pkg/orchestrator"
	"code-agent/pkg/reranker"
	"code-agent/pkg/storage"
	"code-agent/pkg/tika"
	"code-agent/pkg/token"

	"github.com/gin-gonic/gin"
)

// main bootstraps infrastructure dependencies and starts the HTTP server.
func main() {
	configPath := strings.TrimSpace(os.Getenv("CODEAGENT_CONFIG"))
	if configPath == "" {
		configPath = "./configs/server.yaml"
	}
	serverconfig.Init(configPath)
	cfg := serverconfig.Conf

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
	} else if cfg.Memory.Enabled {
		if err := es.EnsureMemoryIndex(cfg.Memory.MemoryIndexName, cfg.Embedding.Dimensions); err != nil {
			log.Warnf("failed to init memory index — memory endpoints degraded: %v", err)
		}
	}
	kafka.InitProducer(cfg.Kafka)

	userRepository := repository.NewUserRepository(database.DB)
	orgTagRepo := repository.NewOrgTagRepository(database.DB)
	uploadRepo := repository.NewUploadRepository(database.DB, database.RDB)
	conversationRepo := repository.NewConversationRepository(database.RDB)
	docVectorRepo := repository.NewDocumentVectorRepository(database.DB)
	pipelineTaskRepo := repository.NewPipelineTaskRepository(database.DB)
	memoryRepo := repository.NewMemoryRepository(database.DB)

	jwtManager := token.NewJWTManager(cfg.JWT.Secret, cfg.JWT.AccessTokenExpireHours, cfg.JWT.RefreshTokenExpireDays)
	tikaClient := tika.NewClient(cfg.Tika)
	embeddingClient := embedding.NewClient(cfg.Embedding)
	orchestratorClient := orchestrator.NewClient(cfg.AI.Orchestrator)
	orchestratorMemoryClient := orchestrator.NewMemoryClient(cfg.AI.Orchestrator)
	ingestionClient := orchestrator.NewIngestionClient(cfg.AI.Orchestrator)
	rerankerClient := reranker.NewClient(cfg.Reranker)

	userService := service.NewUserService(userRepository, orgTagRepo, jwtManager)
	adminService := service.NewAdminService(orgTagRepo, userRepository, conversationRepo, pipelineTaskRepo, uploadRepo)
	uploadService := service.NewUploadService(uploadRepo, userRepository, cfg.MinIO)
	documentService := service.NewDocumentService(uploadRepo, userRepository, orgTagRepo, docVectorRepo, pipelineTaskRepo, cfg.MinIO, cfg.Elasticsearch.IndexName, tikaClient)
	searchService := service.NewSearchService(
		embeddingClient,
		rerankerClient,
		es.ESClient,
		userService,
		uploadRepo,
		cfg.Elasticsearch.IndexName,
		cfg.Retrieval,
	)
	conversationService := service.NewConversationService(conversationRepo)
	memoryService := service.NewMemoryService(memoryRepo, embeddingClient, orchestratorMemoryClient, rerankerClient, es.ESClient, cfg.Memory)
	orchestratorSupportService := service.NewOrchestratorSupportService(searchService, memoryService, conversationRepo, rerankerClient)
	chatService := service.NewChatService(searchService, memoryService, conversationRepo, orchestratorClient)

	processor := pipeline.NewProcessor(
		tikaClient,
		embeddingClient,
		cfg.Elasticsearch,
		cfg.MinIO,
		cfg.Embedding,
		cfg.Kafka,
		uploadRepo,
		docVectorRepo,
		ingestionClient,
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
	r.Use(middleware.RequestLogger(), gin.Recovery())
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

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
			search.GET("/hybrid", handler.NewSearchHandler(searchService).HybridSearch)
		}

		conversation := apiV1.Group("/users/conversation")
		conversation.Use(middleware.AuthMiddleware(jwtManager, userService))
		{
			conversation.GET("", handler.NewConversationHandler(conversationService).GetConversations)
		}

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
		internalGroup.Use(middleware.InternalAuthMiddleware())
		{
			orchHandler := handler.NewOrchestratorHandler(orchestratorSupportService)
			internalGroup.POST("/orchestrator/session", orchHandler.LoadSession)
			internalGroup.POST("/orchestrator/retrieve", orchHandler.RetrieveContext)
			internalGroup.POST("/orchestrator/prompt-context", orchHandler.PreparePromptContext)
			internalGroup.POST("/orchestrator/knowledge-search", orchHandler.SearchKnowledge)
			internalGroup.POST("/orchestrator/memory-search", orchHandler.SearchMemory)
			internalGroup.POST("/orchestrator/rerank-context", orchHandler.RerankContext)
			internalGroup.POST("/orchestrator/persist", orchHandler.PersistTurn)
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
