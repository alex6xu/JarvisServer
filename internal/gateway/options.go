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
	// WorkspacesRoot is the local directory for Coder workspaces (default: <cwd>/workspaces).
	WorkspacesRoot string
}

func (o Options) withDefaults() Options {
	if o.Addr == "" {
		o.Addr = ":8080"
	}
	if o.Model == "" {
		o.Model = "openrouter/free"
	}
	if o.AuthMode == "" {
		o.AuthMode = "none"
	}
	return o
}
