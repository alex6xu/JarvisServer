package gateway

// Options configures the HTTP gateway server.
type Options struct {
	// Addr is the listen address (default ":8080").
	Addr string
	// Cwd is the working directory for tool roots and session attribution.
	Cwd string
	// Model is the default model id when a chat request omits model.
	Model string
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
	// PIGO_ADMIN_PASSWORD is used when this is empty.
	AdminPassword string
	// WorkspacesRoot is the local directory for Coder workspaces (default: <cwd>/workspaces).
	WorkspacesRoot string
	// DatabasePath is the SQLite file used for chat and provider audit records.
	// The default is <cwd>/.pigo/gateway.db.
	DatabasePath       string
	AuditRetentionDays int
	AuditMaxBodyBytes  int
}

func (o Options) withDefaults() Options {
	if o.Addr == "" {
		o.Addr = ":8080"
	}
	if o.Model == "" {
		o.Model = "openrouter/free"
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
	return o
}
