package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"code-agent/internal/admission"
	"code-agent/internal/handler"
	"code-agent/internal/localidentity"
	"code-agent/internal/memory"
	"code-agent/internal/middleware"
	harnessorch "code-agent/internal/orchestrator"
	"code-agent/internal/sandbox"
	"code-agent/internal/serverconfig"
	"code-agent/internal/service"
	"code-agent/internal/session"
	"code-agent/internal/telemetry/genai"
	"code-agent/pkg/log"
	"code-agent/pkg/token"

	"github.com/gin-gonic/gin"
)

func runLocalCore(cfg serverconfig.Config) error {
	host := cfg.Server.Host
	if host == "" {
		host = "127.0.0.1"
	}
	if address := net.ParseIP(host); address == nil || !address.IsLoopback() {
		return errors.New("local core requires a loopback listen address")
	}
	telemetry := genai.NewTelemetry(context.Background())
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := telemetry.Shutdown(ctx); err != nil {
			log.Errorf("local trace exporter shutdown failed")
		}
	}()
	ledger, err := session.OpenSQLiteEventLog(cfg.Harness.SessionLedgerPath)
	if err != nil {
		return err
	}
	defer ledger.Close()
	identity, err := localidentity.Open(cfg.Harness.IdentityPath, os.Getenv("CODE_AGENT_LOCAL_SETUP_USER"), os.Getenv("CODE_AGENT_LOCAL_SETUP_PASSWORD"))
	if err != nil {
		return err
	}
	defer identity.Close()
	jwtManager := token.NewJWTManager(cfg.JWT.Secret, cfg.JWT.AccessTokenExpireHours, cfg.JWT.RefreshTokenExpireDays)
	users := service.NewLocalUserService(identity, jwtManager, identity)
	hub := handler.NewWebSocketHub()
	workbench := session.NewWorkbench(ledger, hub)
	tickets := session.NewWebSocketTickets(30 * time.Second)
	gin.SetMode(cfg.Server.Mode)
	router := gin.New()
	router.Use(middleware.RedactedRecovery(), localOriginBoundary(cfg.Server.AllowedOrigins), corsMiddleware(cfg.Server.AllowedOrigins))
	if cfg.Server.FrontendDir != "" {
		assets, err := mountBrowserAssets(router, cfg.Server.FrontendDir)
		if err != nil {
			return errors.New("configured browser assets are unavailable")
		}
		defer assets.Close()
	}
	api := router.Group("/api/v1")
	registerUserRoutes(api, users, jwtManager)
	optionalUnavailable := func(c *gin.Context) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "state": "degraded", "message": "optional services are disabled in the local profile"})
	}
	for _, prefix := range []string{"/upload", "/documents", "/search", "/chat", "/memory", "/users/conversation"} {
		api.Any(prefix, middleware.AuthMiddleware(jwtManager, users), optionalUnavailable)
		api.Any(prefix+"/*path", middleware.AuthMiddleware(jwtManager, users), optionalUnavailable)
	}
	var tasks session.TaskWorkspaceModule
	var workspaces *session.TaskWorkspaces
	var taskRuntime *sandbox.OpenSandboxRunner
	var taskConfig sandbox.Config
	if cfg.Harness.RepositoryRoot != "" && cfg.Harness.TaskWorkspaceRoot != "" {
		workspaces = session.NewTaskWorkspaces(workbench, identity.OwnerID, cfg.Harness.RepositoryRoot, cfg.Harness.TaskWorkspaceRoot)
		if os.Getenv("CODE_AGENT_TASK_RUNTIME") == "opensandbox" {
			toolchain := os.Getenv("CODE_AGENT_TASK_TOOLCHAIN")
			if toolchain == "" {
				toolchain = "go"
			}
			if toolchain != "go" && toolchain != "python" {
				return errors.New("approved task toolchain must be go or python")
			}
			if toolchain == "go" {
				if err := workspaces.SetPublicTestFixtures("9b9fb55def404397748a9fc7e044efe9db1d618e", map[string]string{
					"testdata/key.pem":  "1f2d4e39e1a799bd668e0fa10fde499505048a4ea6ba0828b0343edc848b2a16",
					"testdata/cert.pem": "f089d14120e9f344fa13a9e45f46098bd8c19c1b3d883526edd8fc5270810bb8",
				}); err != nil {
					return err
				}
			}
			taskConfig = sandbox.Config{Backend: sandbox.BackendOpenSandbox, TrustRoot: cfg.Harness.TaskWorkspaceRoot, Toolchain: toolchain, Image: os.Getenv("CODE_AGENT_TASK_IMAGE"), OpenSandboxManifest: os.Getenv("CODE_AGENT_OPENSANDBOX_MANIFEST"), OpenSandboxManifestSHA256: os.Getenv("CODE_AGENT_OPENSANDBOX_MANIFEST_SHA256")}
			taskRuntime = sandbox.NewOpenSandboxRunner(taskConfig)
			budget := admission.NewBudget(ledger)
			workspaces.WithBaselineRuntime(session.TaskBaselineRuntime{Toolchain: toolchain, Runner: taskRuntime, Ready: taskRuntime.Ready,
				Acquire: func(ctx context.Context, owner uint, id string) error {
					_, err := budget.AcquireTask(ctx, owner, id)
					return err
				},
				Finish: func(ctx context.Context, owner uint, id string) error {
					_, err := budget.FinishTask(ctx, owner, id)
					return err
				},
			})
		}
		tasks = workspaces
	}
	provider := admission.ProviderFromEnv()
	gateway := admission.NewGateway(admission.NewBudget(ledger), provider)
	modelWorkspace := localModelWorkspaceAdmission(workspaces, admission.NewBudget(ledger))
	gateway.SetTaskAdmission(func(ctx context.Context, id string) error {
		if workspaces == nil {
			return session.ErrContinuationUnavailable
		}
		owner, err := identity.OwnerID(ctx)
		if err != nil {
			return err
		}
		_, err = modelWorkspace(ctx, owner, id)
		return err
	})
	if err := gateway.Start(); err != nil {
		return errors.New("model admission initialization unavailable")
	}
	defer gateway.Close()
	if workspaces != nil {
		// The existing task scope fences proposal testing without another model call.
		workspaces.WithProposalScope(gateway.BindExistingTask)
	}
	continuation := session.NewContinuationSlot()
	executors := newContinuationToolExecutors(cfg.Harness.TaskWorkspaceRoot)
	executors.sandbox = newLazySandboxRunner(taskConfig)
	executors.taskWorkspaces, executors.taskOwner = workspaces, identity.OwnerID
	executors.SetTracer(telemetry)
	defer executors.Close()
	target := strings.TrimSpace(os.Getenv("CODE_AGENT_ORCHESTRATOR_ADDR"))
	if target == "" {
		target = "127.0.0.1:50051"
	}
	supervisor := session.NewContinuationSupervisor(continuation, 5*time.Second, func(ctx context.Context) (session.ContinuationModule, error) {
		if provider.Validate() != nil || taskRuntime == nil || taskRuntime.Ready(ctx) != nil {
			return nil, session.ErrContinuationUnavailable
		}
		client, err := harnessorch.NewClient(target)
		if err != nil {
			return nil, session.ErrContinuationUnavailable
		}
		client.SetTracer(telemetry)
		client.SetModelScopeFactory(gateway.BindActor)
		memories := memory.NewLedgerMemory(ledger, client.ReflectMemory)
		memories.SetTracer(telemetry)
		runner := session.NewSessionRunner(workbench, client, executors, session.SessionRunnerOptions{WorkerID: "local:" + cfg.Server.Port, Permissions: continuationPermissionController(), Memory: memories, Tracer: telemetry, Workspace: modelWorkspace})
		return &managedContinuation{runner: runner, client: client}, nil
	})
	supervisor.Start(context.Background())
	defer supervisor.Close()
	router.GET("/healthz", healthzHandlerWithContinuation(func() string {
		if continuation.Available() {
			return "connected"
		}
		return "disabled"
	}, supervisor.Status))
	(sessionHTTP{workbench: workbench, hub: hub, tickets: tickets, tasks: tasks, continuation: continuation, modelGateway: gateway}).register(router, api, users, jwtManager)
	api.GET("/capabilities", middleware.AuthMiddleware(jwtManager, users), func(c *gin.Context) {
		capabilities := map[string]CapabilityStatus{
			"identity":  {State: "ready", Reason: "local persistent authentication"},
			"history":   {State: "ready", Reason: "canonical Session Ledger"},
			"execution": {State: "blocked", Reason: "provider, sandbox and budget admission are required"},
			"rag":       {State: "degraded", Reason: "optional services are disabled in the local profile"},
			"trace":     {State: "unknown", Reason: "trace backend has not been verified"},
		}
		owner, _ := identity.OwnerID(c.Request.Context())
		addModelAdmissionCapabilities(c.Request.Context(), ledger, capabilities, owner)
		capabilities["sandbox"] = CapabilityStatus{State: "blocked", Reason: "local task runtime is not attached"}
		if taskRuntime != nil {
			if taskRuntime.Ready(c.Request.Context()) == nil {
				capabilities["sandbox"] = CapabilityStatus{State: "ready", Reason: "approved OpenSandbox baseline profile"}
			} else {
				capabilities["sandbox"] = CapabilityStatus{State: "blocked", Reason: "approved OpenSandbox prerequisites are unavailable"}
			}
		}
		if provider.Validate() == nil && continuation.Available() && capabilities["sandbox"].State == "ready" && capabilities["budget"].Tokens != nil && capabilities["budget"].State != "blocked" {
			capabilities["execution"] = CapabilityStatus{State: "ready", Reason: "Go model admission and approved task runtime are attached"}
		}
		c.JSON(http.StatusOK, gin.H{"code": http.StatusOK, "data": capabilities})
	})
	return serveHTTPServer(&http.Server{Addr: net.JoinHostPort(host, cfg.Server.Port), Handler: router, ReadHeaderTimeout: 5 * time.Second})
}

func localModelWorkspaceAdmission(workspaces *session.TaskWorkspaces, budget *admission.Budget) func(context.Context, uint, string) (session.TaskModelWorkspace, error) {
	return func(ctx context.Context, owner uint, id string) (session.TaskModelWorkspace, error) {
		batch, err := budget.Inspect(ctx, owner)
		if err != nil {
			return session.TaskModelWorkspace{}, err
		}
		if batch.ActiveCalls != 0 {
			return session.TaskModelWorkspace{}, fmt.Errorf("%w: wait for the active model call to finish", admission.ErrRelayBusy)
		}
		if batch.TaskID != "" && batch.TaskID != id {
			return session.TaskModelWorkspace{}, admission.ErrTaskBusy
		}
		return workspaces.ModelWorkspace(ctx, owner, id)
	}
}

func addModelAdmissionCapabilities(ctx context.Context, ledger session.EventLog, capabilities map[string]CapabilityStatus, owner uint) {
	provider := admission.ProviderFromEnv()
	reason := ""
	state := "blocked"
	switch {
	case provider.APIKey == "":
		reason = "model credentials are missing"
	case provider.Model == "" || provider.BaseURL == "":
		reason = "model and endpoint are missing"
	case provider.InputLimit <= 0 || provider.OutputLimit <= 0:
		reason = "model token bounds are missing"
	case provider.Validate() != nil:
		reason = "model profile is invalid"
	default:
		state, reason = "unknown", "model profile configured; live validity is not confirmed"
	}
	capabilities["provider"] = CapabilityStatus{State: state, Reason: reason}
	batch, err := admission.NewBudget(ledger).Inspect(ctx, owner)
	if err != nil {
		capabilities["budget"] = CapabilityStatus{State: "unknown", Reason: "token Ledger is unavailable; restore it before calling a model"}
		return
	}
	budget := CapabilityStatus{State: "ready", Reason: "provider enforces budget; local usage is audited", Tokens: &TokenBatchStatus{
		BatchID: batch.ID, Limit: batch.LimitTokens, Used: batch.UsedTokens, Reserved: batch.ReservedTokens, CostStatus: batch.CostStatus,
	}}
	if batch.TaskID != "" {
		if _, err := session.NewWorkbench(ledger, nil).Get(ctx, owner, batch.TaskID); err == nil {
			budget.Tokens.TaskID = batch.TaskID
		}
	}
	switch {
	case batch.ActiveCalls > 0:
		budget.State, budget.Reason = "blocked", "a model attempt is still active; wait or recover its execution"
	case batch.UnknownUsage:
		budget.Reason = "provider enforces budget; some usage is unconfirmed"
	case batch.ID == "":
		budget.State, budget.Reason = "unknown", "token batch initializes on the first admitted call"
	}
	capabilities["budget"] = budget
}

func localOriginBoundary(rawOrigins string) gin.HandlerFunc {
	allowed := map[string]bool{}
	for _, origin := range strings.Split(rawOrigins, ",") {
		allowed[strings.TrimSpace(origin)] = true
	}
	return func(c *gin.Context) {
		host := c.Request.Host
		if value, _, err := net.SplitHostPort(host); err == nil {
			host = value
		}
		address := net.ParseIP(strings.Trim(host, "[]"))
		if host != "localhost" && (address == nil || !address.IsLoopback()) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": "local core host is not permitted"})
			return
		}
		if origin := c.GetHeader("Origin"); origin != "" && !allowed[origin] {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": "browser origin is not permitted"})
			return
		}
		c.Next()
	}
}

func serveHTTPServer(server *http.Server) error {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-quit:
		log.Info("shutdown signal received")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return server.Shutdown(ctx)
}
