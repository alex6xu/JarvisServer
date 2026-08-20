package gateway

import (
	"fmt"
	"time"

	"github.com/zeromicro/go-zero/rest"
)

// Config is the go-zero gateway configuration (etc/gateway.yaml).
type Config struct {
	rest.RestConf
	Agent  AgentConf  `json:",optional"`
	GitHub GitHubConf `json:",optional"`
}

// GitHubConf configures the optional GitHub OAuth and repository integration.
type GitHubConf struct {
	ClientID       string `json:",optional"`
	ClientSecret   string `json:",optional"`
	RedirectURL    string `json:",optional"`
	Scopes         string `json:",optional"`
	APIBaseURL     string `json:",optional"`
	WebBaseURL     string `json:",optional"`
	TokenKey       string `json:",optional"`
	GitTimeoutSecs int    `json:",default=300"`
}

// AgentConf holds jarvis agent / auth settings layered on RestConf.
type AgentConf struct {
	Cwd                      string `json:",optional"`
	BaseURL                  string `json:",optional"`
	Protocol                 string `json:",optional"`
	ProviderName             string `json:",optional"`
	APIKey                   string `json:",optional"`
	ThinkingLevel            string `json:",optional"`
	Approve                  bool   `json:",optional"`
	NoTools                  bool   `json:",optional"`
	NoSkills                 bool   `json:",optional"`
	AuthMode                 string `json:",default=token"`
	APIToken                 string `json:",optional"`
	AdminPassword            string `json:",optional"`
	AllowRegistration        bool   `json:",optional"`
	WorkspacesRoot           string `json:",optional"`
	WorkspaceUploadMaxBytes  int64  `json:",default=104857600"`
	WorkspaceMaxBytes        int64  `json:",default=104857600"`
	WorkspaceMaxFileBytes    int64  `json:",default=10485760"`
	DatabasePath             string `json:",optional"`
	AuditRetentionDays       int    `json:",default=30"`
	AuditMaxBodyBytes        int    `json:",default=1048576"`
	RunTimeoutSeconds        int    `json:",default=1800"`
	AllowPrivateProviderURLs bool   `json:",optional"`
}

// ToOptions maps Config into the existing Service Options.
func (c Config) ToOptions() Options {
	host := c.Host
	if host == "" {
		host = "127.0.0.1"
	}
	addr := fmt.Sprintf("%s:%d", host, c.Port)
	if host == "0.0.0.0" {
		addr = fmt.Sprintf(":%d", c.Port)
	}
	return Options{
		Addr:                     addr,
		Cwd:                      c.Agent.Cwd,
		BaseURL:                  c.Agent.BaseURL,
		Protocol:                 c.Agent.Protocol,
		ProviderName:             c.Agent.ProviderName,
		APIKey:                   c.Agent.APIKey,
		ThinkingLevel:            c.Agent.ThinkingLevel,
		Approve:                  c.Agent.Approve,
		NoTools:                  c.Agent.NoTools,
		NoSkills:                 c.Agent.NoSkills,
		AuthMode:                 c.Agent.AuthMode,
		APIToken:                 c.Agent.APIToken,
		AdminPassword:            c.Agent.AdminPassword,
		AllowRegistration:        c.Agent.AllowRegistration,
		WorkspacesRoot:           c.Agent.WorkspacesRoot,
		WorkspaceUploadMaxBytes:  c.Agent.WorkspaceUploadMaxBytes,
		WorkspaceMaxBytes:        c.Agent.WorkspaceMaxBytes,
		WorkspaceMaxFileBytes:    c.Agent.WorkspaceMaxFileBytes,
		DatabasePath:             c.Agent.DatabasePath,
		AuditRetentionDays:       c.Agent.AuditRetentionDays,
		AuditMaxBodyBytes:        c.Agent.AuditMaxBodyBytes,
		RunTimeout:               time.Duration(c.Agent.RunTimeoutSeconds) * time.Second,
		AllowPrivateProviderURLs: c.Agent.AllowPrivateProviderURLs,
		GitHubClientID:           c.GitHub.ClientID,
		GitHubClientSecret:       c.GitHub.ClientSecret,
		GitHubRedirectURL:        c.GitHub.RedirectURL,
		GitHubScopes:             c.GitHub.Scopes,
		GitHubAPIBaseURL:         c.GitHub.APIBaseURL,
		GitHubWebBaseURL:         c.GitHub.WebBaseURL,
		GitHubTokenKey:           c.GitHub.TokenKey,
		GitHubGitTimeout:         time.Duration(c.GitHub.GitTimeoutSecs) * time.Second,
	}.withDefaults()
}
