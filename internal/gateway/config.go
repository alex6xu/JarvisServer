package gateway

import (
	"fmt"

	"github.com/zeromicro/go-zero/rest"
)

// Config is the go-zero gateway configuration (etc/gateway.yaml).
type Config struct {
	rest.RestConf
	Agent AgentConf `json:",optional"`
}

// AgentConf holds pigo agent / auth settings layered on RestConf.
type AgentConf struct {
	Cwd           string `json:",optional"`
	Model         string `json:",default=openrouter/free"`
	BaseURL       string `json:",optional"`
	Protocol      string `json:",optional"`
	ProviderName  string `json:",optional"`
	APIKey        string `json:",optional"`
	ThinkingLevel string `json:",optional"`
	Approve       bool   `json:",default=true"`
	NoTools       bool   `json:",optional"`
	NoSkills      bool   `json:",optional"`
	AuthMode      string `json:",default=none"`
	APIToken      string `json:",optional"`
	WorkspacesRoot string `json:",optional"`
}

// ToOptions maps Config into the existing Service Options.
func (c Config) ToOptions() Options {
	host := c.Host
	if host == "" {
		host = "0.0.0.0"
	}
	addr := fmt.Sprintf("%s:%d", host, c.Port)
	if host == "0.0.0.0" {
		addr = fmt.Sprintf(":%d", c.Port)
	}
	return Options{
		Addr:          addr,
		Cwd:           c.Agent.Cwd,
		Model:         c.Agent.Model,
		BaseURL:       c.Agent.BaseURL,
		Protocol:      c.Agent.Protocol,
		ProviderName:  c.Agent.ProviderName,
		APIKey:        c.Agent.APIKey,
		ThinkingLevel: c.Agent.ThinkingLevel,
		Approve:       c.Agent.Approve,
		NoTools:       c.Agent.NoTools,
		NoSkills:      c.Agent.NoSkills,
		AuthMode:       c.Agent.AuthMode,
		APIToken:       c.Agent.APIToken,
		WorkspacesRoot: c.Agent.WorkspacesRoot,
	}.withDefaults()
}
