package gateway

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest"
)

// registerRoutes mounts CodeGateway-compatible HTTP routes on the go-zero server.
func registerRoutes(server *rest.Server, svc *Service) {
	server.AddRoutes(apiRoutes(svc))
	server.AddRoutes(sseRoutes(svc), rest.WithSSE(), rest.WithTimeout(0))
}

func apiRoutes(svc *Service) []rest.Route {
	return []rest.Route{
		{Method: http.MethodGet, Path: "/healthz", Handler: svc.handleHealthz},
		{Method: http.MethodGet, Path: "/v1/models", Handler: svc.handleModels},

		// Auth
		{Method: http.MethodGet, Path: "/v1/auth/config", Handler: svc.handleAuthConfig},
		{Method: http.MethodGet, Path: "/v1/auth/me", Handler: svc.handleAuthMe},
		{Method: http.MethodPost, Path: "/v1/auth/login", Handler: svc.handleAuthLogin},
		{Method: http.MethodPost, Path: "/v1/auth/register", Handler: svc.handleAuthRegister},
		{Method: http.MethodPost, Path: "/v1/auth/logout", Handler: svc.handleAuthLogout},
		{Method: http.MethodPost, Path: "/v1/auth/change-password", Handler: svc.handleChangePassword},

		// Agent sessions / chat / import (static paths before :param)
		{Method: http.MethodPost, Path: "/v1/agent/chat", Handler: svc.handleChat},
		{Method: http.MethodPost, Path: "/v1/agent/runs/:runId/cancel", Handler: svc.handleCancelRun},
		{Method: http.MethodGet, Path: "/v1/agent/sessions", Handler: svc.handleListSessions},
		{Method: http.MethodPost, Path: "/v1/agent/sessions/import/preview", Handler: svc.handleImportPreview},
		{Method: http.MethodPost, Path: "/v1/agent/sessions/import", Handler: svc.handleImportSession},
		{Method: http.MethodGet, Path: "/v1/agent/sessions/:sessionId", Handler: svc.handleGetSession},

		// Tags (static before :slug)
		{Method: http.MethodGet, Path: "/v1/agent/tags", Handler: svc.handleListTags},
		{Method: http.MethodGet, Path: "/v1/agent/tags/overview", Handler: svc.handleTagsOverview},
		{Method: http.MethodPost, Path: "/v1/agent/tags/retag", Handler: svc.handleRetag},
		{Method: http.MethodGet, Path: "/v1/agent/tags/:slug", Handler: svc.handleGetTag},

		// Workspaces (static before :id)
		{Method: http.MethodGet, Path: "/v1/workspaces", Handler: svc.handleListWorkspaces},
		{Method: http.MethodGet, Path: "/v1/workspaces/upload-limits", Handler: svc.handleWorkspaceUploadLimits},
		{Method: http.MethodPost, Path: "/v1/workspaces/upload", Handler: svc.handleUploadWorkspace},
		{Method: http.MethodGet, Path: "/v1/workspaces/:id/download", Handler: svc.handleDownloadWorkspace},
		{Method: http.MethodDelete, Path: "/v1/workspaces/:id", Handler: svc.handleDeleteWorkspace},

		// GitHub repository integration
		{Method: http.MethodGet, Path: "/v1/github/status", Handler: svc.handleGitHubStatus},
		{Method: http.MethodGet, Path: "/v1/github/authorize", Handler: svc.handleGitHubAuthorize},
		{Method: http.MethodGet, Path: "/v1/github/callback", Handler: svc.handleGitHubCallback},
		{Method: http.MethodPut, Path: "/v1/github/token", Handler: svc.handleGitHubConnectToken},
		{Method: http.MethodDelete, Path: "/v1/github/disconnect", Handler: svc.handleGitHubDisconnect},
		{Method: http.MethodGet, Path: "/v1/github/repos", Handler: svc.handleGitHubRepos},
		{Method: http.MethodPost, Path: "/v1/github/import", Handler: svc.handleGitHubImport},
		{Method: http.MethodPost, Path: "/v1/github/workspaces/:workspaceId/pull", Handler: svc.handleGitHubPull},
		{Method: http.MethodPost, Path: "/v1/github/workspaces/:workspaceId/push", Handler: svc.handleGitHubPush},

		// Claude OAuth stubs
		{Method: http.MethodGet, Path: "/v1/claude/oauth/status", Handler: svc.handleClaudeOAuthStatus},
		{Method: http.MethodGet, Path: "/v1/claude/oauth/authorize", Handler: svc.handleClaudeOAuthAuthorize},
		{Method: http.MethodPost, Path: "/v1/claude/oauth/exchange", Handler: svc.handleClaudeOAuthExchange},
		{Method: http.MethodDelete, Path: "/v1/claude/oauth/disconnect", Handler: svc.handleClaudeOAuthDisconnect},

		// ASR stubs
		{Method: http.MethodGet, Path: "/v1/asr/status", Handler: svc.handleASRStatus},
		{Method: http.MethodPost, Path: "/v1/asr", Handler: svc.handleASR},

		// Admin accounts
		{Method: http.MethodGet, Path: "/v1/admin/accounts", Handler: svc.handleListAccounts},
		{Method: http.MethodPost, Path: "/v1/admin/accounts", Handler: svc.handleCreateAccount},
		{Method: http.MethodDelete, Path: "/v1/admin/accounts/:id", Handler: svc.handleDeleteAccount},

		// Admin tokens
		{Method: http.MethodGet, Path: "/v1/admin/tokens", Handler: svc.handleListTokens},
		{Method: http.MethodPost, Path: "/v1/admin/tokens", Handler: svc.handleCreateToken},
		{Method: http.MethodPut, Path: "/v1/admin/tokens/:id", Handler: svc.handleUpdateToken},
		{Method: http.MethodDelete, Path: "/v1/admin/tokens/:id", Handler: svc.handleDeleteToken},

		// Admin providers (static before :id)
		{Method: http.MethodGet, Path: "/v1/admin/providers", Handler: svc.handleListProviders},
		{Method: http.MethodPost, Path: "/v1/admin/providers", Handler: svc.handleCreateProvider},
		{Method: http.MethodPost, Path: "/v1/admin/providers/fetch-models", Handler: svc.handleFetchModelsBody},
		{Method: http.MethodPut, Path: "/v1/admin/providers/:id", Handler: svc.handleUpdateProvider},
		{Method: http.MethodDelete, Path: "/v1/admin/providers/:id", Handler: svc.handleDeleteProvider},
		{Method: http.MethodPut, Path: "/v1/admin/providers/:id/set-default", Handler: svc.handleSetDefaultProvider},
		{Method: http.MethodPut, Path: "/v1/admin/providers/:id/status", Handler: svc.handleSetProviderStatus},
		{Method: http.MethodPost, Path: "/v1/admin/providers/:id/fetch-models", Handler: svc.handleFetchProviderModels},
		{Method: http.MethodPost, Path: "/v1/admin/providers/:id/probe", Handler: svc.handleProbeProvider},
		{Method: http.MethodGet, Path: "/v1/admin/routes/preview", Handler: svc.handleRoutePreview},
		{Method: http.MethodGet, Path: "/v1/admin/route-policies", Handler: svc.handleListRoutePolicies},
		{Method: http.MethodPost, Path: "/v1/admin/route-policies", Handler: svc.handlePublishRoutePolicy},
		{Method: http.MethodPut, Path: "/v1/admin/route-policies/:id", Handler: svc.handlePublishRoutePolicy},
		{Method: http.MethodGet, Path: "/v1/admin/runs/:runId/attempts", Handler: svc.handleListRunAttempts},

		// Admin stats / logs / route profiles
		{Method: http.MethodGet, Path: "/v1/admin/stats", Handler: svc.handleAdminStats},
		{Method: http.MethodGet, Path: "/v1/admin/request-logs", Handler: svc.handleListRequestLogs},
		{Method: http.MethodGet, Path: "/v1/admin/request-logs/:id", Handler: svc.handleGetRequestLog},
		{Method: http.MethodGet, Path: "/v1/admin/route-profiles", Handler: svc.handleListRouteProfiles},
		{Method: http.MethodPost, Path: "/v1/admin/route-profiles", Handler: svc.handleCreateRouteProfile},
	}
}

func sseRoutes(svc *Service) []rest.Route {
	return []rest.Route{
		{Method: http.MethodGet, Path: "/v1/agent/runs/:runId/events", Handler: svc.handleRunEvents},
	}
}
