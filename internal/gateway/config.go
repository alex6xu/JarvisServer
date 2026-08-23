package gateway

import (
	"fmt"
	"time"

	"github.com/alex6xu/jarvisserver/internal/distributedlog"
	"github.com/zeromicro/go-zero/rest"
)

// Config is the go-zero gateway configuration (etc/gateway.yaml).
type Config struct {
	rest.RestConf
	Agent          AgentConf          `json:",optional"`
	GitHub         GitHubConf         `json:",optional"`
	MarketData     MarketDataConf     `json:",optional"`
	DistributedLog DistributedLogConf `json:",optional"`
}

// MarketDataConf configures public, unauthenticated market-data streams.
type MarketDataConf struct {
	BinanceWSURL                  string                 `json:",optional"`
	OKXWSURL                      string                 `json:",optional"`
	BinanceRESTURL                string                 `json:",optional"`
	OKXRESTURL                    string                 `json:",optional"`
	StockSentimentAPIKey          string                 `json:",optional"`
	StockSentimentAPIURL          string                 `json:",optional"`
	StockSentimentCacheTTLSeconds int                    `json:",default=86400"`
	NewsSentiment                 StockNewsSentimentConf `json:",optional"`
}

type StockNewsSentimentConf struct {
	CacheTTLSeconds int                   `json:",default=1800"`
	MaxResults      int                   `json:",default=12"`
	Anspire         StockNewsProviderConf `json:",optional"`
	Tavily          StockNewsProviderConf `json:",optional"`
	Bocha           StockNewsProviderConf `json:",optional"`
	Brave           StockNewsProviderConf `json:",optional"`
}

type StockNewsProviderConf struct {
	APIKeys string `json:",optional"`
	APIURL  string `json:",optional"`
}

// DistributedLogConf adds fields used to correlate logs across instances.
// Output destination and rotation are configured by the standard Log section.
type DistributedLogConf struct {
	ServiceName string `json:",optional"`
	Environment string `json:",optional"`
	InstanceID  string `json:",optional"`
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
	Cwd                          string `json:",optional"`
	Model                        string `json:",optional"`
	BaseURL                      string `json:",optional"`
	Protocol                     string `json:",optional"`
	ProviderName                 string `json:",optional"`
	APIKey                       string `json:",optional"`
	ThinkingLevel                string `json:",optional"`
	Approve                      bool   `json:",optional"`
	NoTools                      bool   `json:",optional"`
	NoSkills                     bool   `json:",optional"`
	AuthMode                     string `json:",default=token"`
	APIToken                     string `json:",optional"`
	AdminPassword                string `json:",optional"`
	AllowRegistration            bool   `json:",optional"`
	WorkspacesRoot               string `json:",optional"`
	WorkspaceUploadMaxBytes      int64  `json:",default=104857600"`
	WorkspaceMaxBytes            int64  `json:",default=104857600"`
	WorkspaceMaxFileBytes        int64  `json:",default=10485760"`
	DatabasePath                 string `json:",optional"`
	AuditRetentionDays           int    `json:",default=30"`
	AuditMaxBodyBytes            int    `json:",default=1048576"`
	RunTimeoutSeconds            int    `json:",default=1800"`
	AllowPrivateProviderURLs     bool   `json:",optional"`
	AllowPrivateNotificationURLs bool   `json:",optional"`
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
	serviceName := c.DistributedLog.ServiceName
	if serviceName == "" {
		serviceName = c.Name
	}
	environment := c.DistributedLog.Environment
	if environment == "" {
		environment = c.Mode
	}
	return Options{
		Addr:                         addr,
		Cwd:                          c.Agent.Cwd,
		DefaultModel:                 c.Agent.Model,
		BaseURL:                      c.Agent.BaseURL,
		Protocol:                     c.Agent.Protocol,
		ProviderName:                 c.Agent.ProviderName,
		APIKey:                       c.Agent.APIKey,
		ThinkingLevel:                c.Agent.ThinkingLevel,
		Approve:                      c.Agent.Approve,
		NoTools:                      c.Agent.NoTools,
		NoSkills:                     c.Agent.NoSkills,
		AuthMode:                     c.Agent.AuthMode,
		APIToken:                     c.Agent.APIToken,
		AdminPassword:                c.Agent.AdminPassword,
		AllowRegistration:            c.Agent.AllowRegistration,
		WorkspacesRoot:               c.Agent.WorkspacesRoot,
		WorkspaceUploadMaxBytes:      c.Agent.WorkspaceUploadMaxBytes,
		WorkspaceMaxBytes:            c.Agent.WorkspaceMaxBytes,
		WorkspaceMaxFileBytes:        c.Agent.WorkspaceMaxFileBytes,
		DatabasePath:                 c.Agent.DatabasePath,
		AuditRetentionDays:           c.Agent.AuditRetentionDays,
		AuditMaxBodyBytes:            c.Agent.AuditMaxBodyBytes,
		RunTimeout:                   time.Duration(c.Agent.RunTimeoutSeconds) * time.Second,
		AllowPrivateProviderURLs:     c.Agent.AllowPrivateProviderURLs,
		AllowPrivateNotificationURLs: c.Agent.AllowPrivateNotificationURLs,
		GitHubClientID:               c.GitHub.ClientID,
		GitHubClientSecret:           c.GitHub.ClientSecret,
		GitHubRedirectURL:            c.GitHub.RedirectURL,
		GitHubScopes:                 c.GitHub.Scopes,
		GitHubAPIBaseURL:             c.GitHub.APIBaseURL,
		GitHubWebBaseURL:             c.GitHub.WebBaseURL,
		GitHubTokenKey:               c.GitHub.TokenKey,
		GitHubGitTimeout:             time.Duration(c.GitHub.GitTimeoutSecs) * time.Second,
		BinanceMarketWSURL:           c.MarketData.BinanceWSURL,
		OKXMarketWSURL:               c.MarketData.OKXWSURL,
		BinanceMarketRESTURL:         c.MarketData.BinanceRESTURL,
		OKXMarketRESTURL:             c.MarketData.OKXRESTURL,
		StockSentimentAPIKey:         c.MarketData.StockSentimentAPIKey,
		StockSentimentAPIURL:         c.MarketData.StockSentimentAPIURL,
		StockSentimentCacheTTL:       time.Duration(c.MarketData.StockSentimentCacheTTLSeconds) * time.Second,
		StockNewsCacheTTL:            time.Duration(c.MarketData.NewsSentiment.CacheTTLSeconds) * time.Second,
		StockNewsMaxResults:          c.MarketData.NewsSentiment.MaxResults,
		StockNewsProviders:           stockNewsProviderOptionsFromConfig(c.MarketData.NewsSentiment),
		Logger: distributedlog.New(distributedlog.Config{
			Service: serviceName, Environment: environment, InstanceID: c.DistributedLog.InstanceID,
		}),
	}.withDefaults()
}
