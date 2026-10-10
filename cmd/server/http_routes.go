package main

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"code-agent/internal/handler"
	"code-agent/internal/middleware"
	"code-agent/internal/service"
	"code-agent/internal/session"
	"code-agent/internal/worktree"
	"code-agent/pkg/token"

	"github.com/gin-gonic/gin"
)

func mountBrowserAssets(router *gin.Engine, directory string) (*os.Root, error) {
	assets, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	router.GET("/", func(c *gin.Context) { http.ServeFileFS(c.Writer, c.Request, assets.FS(), "index.html") })
	router.GET("/assets/*file", func(c *gin.Context) {
		name := "assets/" + strings.TrimPrefix(c.Param("file"), "/")
		if !fs.ValidPath(name) || strings.HasPrefix(filepath.Base(name), ".") {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		info, err := fs.Stat(assets.FS(), name)
		if err != nil || !info.Mode().IsRegular() {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		http.FileServer(http.FS(assets.FS())).ServeHTTP(c.Writer, c.Request)
	})
	return assets, nil
}

func registerUserRoutes(apiV1 *gin.RouterGroup, userService service.UserService, jwtManager *token.JWTManager) {
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
}

type sessionHTTP struct {
	workbench    session.WorkbenchModule
	hub          *handler.WebSocketHub
	tickets      *session.WebSocketTickets
	worktree     *worktree.Manager
	tasks        session.TaskWorkspaceModule
	continuation *session.ContinuationSlot
}

func (routes sessionHTTP) register(r *gin.Engine, apiV1 *gin.RouterGroup, userService service.UserService, jwtManager *token.JWTManager) {
	sessions := apiV1.Group("/sessions")
	sessions.Use(middleware.TraceContextMiddleware(), middleware.AuthMiddleware(jwtManager, userService))
	{
		sessionHandler := handler.NewSessionHandlerWithWorktree(routes.workbench, routes.tickets, routes.worktree)
		sessions.POST("", sessionHandler.Create)
		sessions.GET("", sessionHandler.List)
		sessions.GET("/:id", sessionHandler.Get)
		sessions.GET("/:id/runs", sessionHandler.RunHistory)
		sessions.GET("/:id/runs/:runId", sessionHandler.RunDetail)
		sessions.GET("/:id/recovery-manifest", sessionHandler.RecoveryManifest)
		sessions.GET("/:id/workspace-manifest", sessionHandler.WorkspaceManifest)
		sessions.POST("/:id/workspace/restore", sessionHandler.RestoreWorkspace)
		taskHandler := handler.NewTaskWorkspaceHandler(routes.workbench, routes.tasks)
		sessions.GET("/:id/task-workspace", taskHandler.Inspect)
		sessions.POST("/:id/task-workspace/prepare", taskHandler.Prepare)
		sessions.PUT("/:id/title", sessionHandler.UpdateTitle)
		sessions.PUT("/:id/status", sessionHandler.UpdateStatus)
		sessions.DELETE("/:id", sessionHandler.Delete)
		sessions.POST("/:id/ws-ticket", sessionHandler.IssueWebSocketTicket)

		eventHandler := handler.NewEventHandlerWithContinuationSlot(routes.workbench, routes.continuation)
		sessions.GET("/:id/events", eventHandler.ListEvents)
		sessions.POST("/:id/events", eventHandler.CreateEvent)
		sessions.POST("/:id/messages", eventHandler.SubmitMessage)
		sessions.GET("/:id/checkpoints", eventHandler.ListCheckpoints)
		sessions.POST("/:id/checkpoints", eventHandler.CreateCheckpoint)
		sessions.POST("/:id/restore/:hash", eventHandler.RestoreCheckpoint)
		sessions.POST("/:id/continue", eventHandler.ContinueSession)
		sessions.POST("/:id/approvals/:runId/:toolCallId", eventHandler.DecideToolApproval)
	}
	// WebSocket uses an opaque one-time ticket, not an access JWT in the URL.
	wsHandler := handler.NewWebSocketHandlerWithWorkbench(routes.hub, routes.workbench, routes.tickets)
	r.GET("/api/v1/sessions/:id/ws", wsHandler.HandleWebSocket)
}
