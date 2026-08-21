package gateway

import (
	"os"
	"time"

	"github.com/alex6xu/jarvisserver/internal/distributedlog"
)

// Options configures the HTTP gateway server.
type Options struct {
	// Addr is the listen address (default ":8080").
	Addr string
	// Cwd is the working directory for tool roots and session attribution.
	Cwd string
	// BaseURL / Protocol / ProviderName mirror jarvis CLI provider selection.
	BaseURL      string
	Protocol     string
	ProviderName string
	// APIKey overrides env/config credentials for the resolved provider.
	APIKey string
	// ThinkingLevel is the optional reasoning effort (off|minimal|…|max).
	ThinkingLevel string
	// Approve grants session-level trust so side-effect tools need no stdin confirm.
	Approve bool
	// NoTools disables built-in / plugin tools for every run.
	NoTools bool
	// NoSkills disables skill discovery.
	NoSkills bool
	// AuthMode is "none" (accept any bearer / auto-issue on login) or "token"
	// (require Authorization matching APIKey when set).
	AuthMode string
	// APIToken is the shared bearer token when AuthMode=token. Empty accepts any.
	APIToken string
	// AdminPassword bootstraps the first dev administrator. Environment
	// JARVIS_ADMIN_PASSWORD is used when this is empty.
	AdminPassword string
	// AllowRegistration enables unauthenticated account self-registration.
	// It is disabled by default; administrators can still create accounts.
	AllowRegistration bool
	// WorkspacesRoot is the local directory for Coder workspaces (default: <cwd>/workspaces).
	WorkspacesRoot string
	// WorkspaceUploadMaxBytes bounds the compressed multipart archive.
	WorkspaceUploadMaxBytes int64
	// WorkspaceMaxBytes bounds the total uncompressed workspace size.
	WorkspaceMaxBytes int64
	// WorkspaceMaxFileBytes bounds one uncompressed workspace file.
	WorkspaceMaxFileBytes int64
	// DatabasePath is the SQLite file used for chat and provider audit records.
	// The default is <cwd>/.jarvis/gateway.db.
	DatabasePath       string
	AuditRetentionDays int
	AuditMaxBodyBytes  int
	// RunTimeout bounds one asynchronous agent run. A negative value disables it.
	RunTimeout time.Duration
	// AllowPrivateProviderURLs permits custom providers on private/link-local networks.
	AllowPrivateProviderURLs bool
	// GitHub OAuth and API settings. A personal access token can still be
	// connected per account when the OAuth client is not configured.
	GitHubClientID     string
	GitHubClientSecret string
	GitHubRedirectURL  string
	GitHubScopes       string
	GitHubAPIBaseURL   string
	GitHubWebBaseURL   string
	// GitHubTokenKey protects stored OAuth/PAT credentials. When empty, a local
	// random key is generated under <cwd>/.jarvis.
	GitHubTokenKey   string
	GitHubGitTimeout time.Duration
	// Logger emits structured, correlation-aware operational events.
	Logger *distributedlog.Logger
}

func (o Options) withDefaults() Options {
	if o.Addr == "" {
		o.Addr = ":8080"
	}
	if o.AuthMode == "" {
		o.AuthMode = "token"
	}
	if o.AuditRetentionDays == 0 {
		o.AuditRetentionDays = 30
	}
	if o.AuditMaxBodyBytes <= 0 {
		o.AuditMaxBodyBytes = 1 << 20
	}
	if o.WorkspaceUploadMaxBytes <= 0 {
		o.WorkspaceUploadMaxBytes = defaultWorkspaceArchiveBytes
	}
	if o.WorkspaceMaxBytes <= 0 {
		o.WorkspaceMaxBytes = defaultWorkspaceUncompressedBytes
	}
	if o.WorkspaceMaxFileBytes <= 0 {
		o.WorkspaceMaxFileBytes = defaultWorkspaceFileBytes
	}
	if o.RunTimeout == 0 {
		o.RunTimeout = 30 * time.Minute
	}
	if o.GitHubClientID == "" {
		o.GitHubClientID = os.Getenv("GITHUB_CLIENT_ID")
	}
	if o.GitHubClientSecret == "" {
		o.GitHubClientSecret = os.Getenv("GITHUB_CLIENT_SECRET")
	}
	if o.GitHubRedirectURL == "" {
		o.GitHubRedirectURL = os.Getenv("GITHUB_REDIRECT_URL")
	}
	if o.GitHubTokenKey == "" {
		o.GitHubTokenKey = os.Getenv("GITHUB_TOKEN_KEY")
	}
	if o.GitHubScopes == "" {
		o.GitHubScopes = "repo read:user"
	}
	if o.GitHubAPIBaseURL == "" {
		o.GitHubAPIBaseURL = "https://api.github.com"
	}
	if o.GitHubWebBaseURL == "" {
		o.GitHubWebBaseURL = "https://github.com"
	}
	if o.GitHubGitTimeout <= 0 {
		o.GitHubGitTimeout = 5 * time.Minute
	}
	if o.Logger == nil {
		o.Logger = distributedlog.New(distributedlog.Config{})
	}
	return o
}
