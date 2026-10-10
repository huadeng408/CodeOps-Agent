package main

import (
	"context"
	"errors"
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
	"code-agent/internal/middleware"
	"code-agent/internal/serverconfig"
	"code-agent/internal/service"
	"code-agent/internal/session"
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
	router.GET("/healthz", healthzHandlerWithContinuation(func() string { return "disabled" }, func() session.ContinuationSupervisorStatus {
		return session.ContinuationSupervisorStatus{LastHealthError: "execution prerequisites are not admitted"}
	}))
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
	if cfg.Harness.RepositoryRoot != "" && cfg.Harness.TaskWorkspaceRoot != "" {
		tasks = session.NewTaskWorkspaces(workbench, identity.OwnerID, cfg.Harness.RepositoryRoot, cfg.Harness.TaskWorkspaceRoot)
	}
	(sessionHTTP{workbench: workbench, hub: hub, tickets: tickets, tasks: tasks, continuation: session.NewContinuationSlot()}).register(router, api, users, jwtManager)
	api.GET("/capabilities", middleware.AuthMiddleware(jwtManager, users), func(c *gin.Context) {
		capabilities := map[string]CapabilityStatus{
			"identity":  {State: "ready", Reason: "local persistent authentication"},
			"history":   {State: "ready", Reason: "canonical Session Ledger"},
			"execution": {State: "blocked", Reason: "provider, sandbox and budget admission are required"},
			"rag":       {State: "degraded", Reason: "optional services are disabled in the local profile"},
			"trace":     {State: "unknown", Reason: "trace backend has not been verified"},
		}
		addModelAdmissionCapabilities(c.Request.Context(), ledger, capabilities)
		capabilities["sandbox"] = CapabilityStatus{State: "blocked", Reason: "local task runtime is not attached"}
		c.JSON(http.StatusOK, gin.H{"code": http.StatusOK, "data": capabilities})
	})
	return serveHTTPServer(&http.Server{Addr: net.JoinHostPort(host, cfg.Server.Port), Handler: router, ReadHeaderTimeout: 5 * time.Second})
}

func addModelAdmissionCapabilities(ctx context.Context, ledger session.EventLog, capabilities map[string]CapabilityStatus) {
	provider := admission.ProviderFromEnv()
	reason := "model admission is not enabled"
	state := "blocked"
	if os.Getenv("CODE_AGENT_MODEL_ADMISSION") == "required" {
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
	}
	capabilities["provider"] = CapabilityStatus{State: state, Reason: reason}
	batch, err := admission.NewBudget(ledger).Inspect(ctx, 1)
	if err != nil {
		capabilities["budget"] = CapabilityStatus{State: "unknown", Reason: "token Ledger is unavailable; restore it before calling a model"}
		return
	}
	budget := CapabilityStatus{State: "ready", Reason: "persistent token admission; price unknown", Tokens: &TokenBatchStatus{
		BatchID: batch.ID, Limit: batch.LimitTokens, Used: batch.UsedTokens, Reserved: batch.ReservedTokens, CostStatus: batch.CostStatus,
	}}
	switch {
	case batch.UnknownUsage:
		budget.State, budget.Reason = "blocked", "token usage is unknown; reconcile retained reservations"
	case batch.ReservedTokens > 0:
		budget.State, budget.Reason = "blocked", "a model attempt is unresolved; wait or reconcile after restart"
	case batch.UsedTokens >= batch.LimitTokens:
		budget.State, budget.Reason = "blocked", "token batch is exhausted"
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
