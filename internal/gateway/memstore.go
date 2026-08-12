package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

// MemStore holds in-process admin/product state expected by the web UI.
type MemStore struct {
	mu sync.RWMutex

	nextAccountID int64
	nextTokenID   int64
	nextProvID    int64
	nextProfileID int64
	nextLogID     int64
	nextTaskID    int64
	nextTagID     int64

	accounts  map[int]*Account
	tokens    map[string]*APIToken
	providers map[int]*Provider
	profiles  map[string]*RouteProfile
	logs      []*RequestLog
	tasks     map[string]*AgentTask
	tags      map[string]*Tag
}

type APIToken struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Key            string `json:"key"`
	Status         int    `json:"status"`
	UnlimitedQuota bool   `json:"unlimited_quota"`
	CreatedAt      string `json:"created_at"`
}

// Provider matches the web ChannelsPage Channel JSON shape.
type Provider struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Type      int    `json:"type"`
	Key       string `json:"key"`
	BaseURL   string `json:"base_url"`
	Models    string `json:"models"`
	Status    int    `json:"status"`
	Weight    int    `json:"weight"`
	Priority  int    `json:"priority"`
	IsDefault int    `json:"is_default"`
	AuthMode  string `json:"auth_mode,omitempty"`
}

type RouteProfile struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Purpose string   `json:"purpose"`
	Models  []string `json:"models"`
}

type RequestLog struct {
	ID               string `json:"id"`
	ProviderID       string `json:"provider_id,omitempty"`
	ProviderName     string `json:"provider_name,omitempty"`
	Model            string `json:"model"`
	Stream           bool   `json:"stream"`
	StatusCode       int    `json:"status_code"`
	Error            string `json:"error,omitempty"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	CachedTokens     int    `json:"cached_tokens"`
	LatencyMs        int    `json:"latency_ms"`
	CreatedAt        string `json:"created_at"`
	RequestBody      string `json:"request_body,omitempty"`
	ResponseBody     string `json:"response_body,omitempty"`
	UserID           int    `json:"user_id,omitempty"`
}

type AgentTask struct {
	ID             string     `json:"id"`
	WorkspaceID    string     `json:"workspace_id"`
	RouteProfileID string     `json:"route_profile_id"`
	Type           string     `json:"type"`
	Prompt         string     `json:"prompt"`
	Status         string     `json:"status"`
	Result         string     `json:"result"`
	Error          string     `json:"error,omitempty"`
	ToolSteps      []ToolStep `json:"tool_steps"`
	CreatedAt      string     `json:"created_at"`
	FinishedAt     string     `json:"finished_at,omitempty"`
}

type Tag struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	UseCount  int    `json:"use_count"`
	UpdatedAt string `json:"updated_at"`
}

type TaggedMessage struct {
	MessageID    string `json:"message_id"`
	SessionID    string `json:"session_id"`
	Content      string `json:"content"`
	Preview      string `json:"preview"`
	Platform     string `json:"platform"`
	SessionTitle string `json:"session_title"`
	CreatedAt    string `json:"created_at"`
	Tags         []struct {
		Slug       string  `json:"slug"`
		Name       string  `json:"name"`
		Kind       string  `json:"kind"`
		Confidence float64 `json:"confidence"`
	} `json:"tags"`
}

func newMemStore(seedModel string) *MemStore {
	m := &MemStore{
		nextAccountID: 1,
		accounts:      make(map[int]*Account),
		tokens:        make(map[string]*APIToken),
		providers:     make(map[int]*Provider),
		profiles:      make(map[string]*RouteProfile),
		tasks:         make(map[string]*AgentTask),
		tags:          make(map[string]*Tag),
	}
	now := time.Now().UTC().Format(time.RFC3339)
	acct := &Account{
		ID: 1, Username: "dev", Email: "dev@localhost", Role: "admin",
		CreatedAt: now, UpdatedAt: now,
	}
	m.accounts[1] = acct
	m.nextAccountID = 2

	m.profiles["rp_1"] = &RouteProfile{
		ID: "rp_1", Name: "default", Purpose: "general", Models: []string{seedModel},
	}
	m.nextProfileID = 2
	_ = m.loadProvidersFromDisk()
	return m
}

func newID(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b[:]))
}

func (m *MemStore) listAccounts() []Account {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Account, 0, len(m.accounts))
	for _, a := range m.accounts {
		out = append(out, *a)
	}
	return out
}

func (m *MemStore) createAccount(username, email, role, _ string) (*Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.accounts {
		if a.Username == username {
			return nil, fmt.Errorf("username already exists")
		}
	}
	if role == "" {
		role = "user"
	}
	if email == "" {
		email = username + "@localhost"
	}
	nid := 0
	for _, a := range m.accounts {
		if a.ID > nid {
			nid = a.ID
		}
	}
	nid++
	now := time.Now().UTC().Format(time.RFC3339)
	a := &Account{ID: nid, Username: username, Email: email, Role: role, CreatedAt: now, UpdatedAt: now}
	m.accounts[nid] = a
	cp := *a
	return &cp, nil
}

func (m *MemStore) deleteAccount(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == 1 {
		return fmt.Errorf("cannot delete primary account")
	}
	if _, ok := m.accounts[id]; !ok {
		return fmt.Errorf("account not found")
	}
	delete(m.accounts, id)
	return nil
}

func (m *MemStore) listTokens() []APIToken {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]APIToken, 0, len(m.tokens))
	for _, t := range m.tokens {
		out = append(out, *t)
	}
	return out
}

func (m *MemStore) createToken(name string) APIToken {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := &APIToken{
		ID: newID("tok"), Name: name, Key: "sk-" + newID("key"),
		Status: 1, UnlimitedQuota: true, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	m.tokens[t.ID] = t
	return *t
}

func (m *MemStore) updateTokenStatus(id string, status int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[id]
	if !ok {
		return fmt.Errorf("token not found")
	}
	t.Status = status
	return nil
}

func (m *MemStore) deleteToken(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tokens[id]; !ok {
		return fmt.Errorf("token not found")
	}
	delete(m.tokens, id)
	return nil
}

func (m *MemStore) listProviders() []Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Provider, 0, len(m.providers))
	for _, p := range m.providers {
		out = append(out, *p)
	}
	return out
}

func (m *MemStore) upsertProvider(id int, p Provider) Provider {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id <= 0 {
		m.nextProvID++
		p.ID = int(m.nextProvID)
	} else {
		p.ID = id
		if int64(id) > m.nextProvID {
			m.nextProvID = int64(id)
		}
	}
	if p.Status == 0 {
		p.Status = 1
	}
	if p.AuthMode == "" {
		p.AuthMode = "api_key"
	}
	if p.IsDefault == 1 {
		for _, x := range m.providers {
			x.IsDefault = 0
		}
	}
	// Preserve key when update omits it (edit form may send empty key).
	if existing, ok := m.providers[p.ID]; ok && strings.TrimSpace(p.Key) == "" {
		p.Key = existing.Key
	}
	cp := p
	m.providers[p.ID] = &cp
	return p
}

func (m *MemStore) deleteProvider(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.providers[id]; !ok {
		return fmt.Errorf("provider not found")
	}
	delete(m.providers, id)
	return nil
}

func (m *MemStore) setDefaultProvider(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.providers[id]
	if !ok {
		return fmt.Errorf("provider not found")
	}
	for _, x := range m.providers {
		x.IsDefault = 0
	}
	p.IsDefault = 1
	return nil
}

func (m *MemStore) getProvider(id int) (*Provider, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.providers[id]
	if !ok {
		return nil, false
	}
	cp := *p
	return &cp, true
}

func (m *MemStore) defaultProvider() *Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var fallback *Provider
	for _, p := range m.providers {
		if p.Status == 0 {
			continue
		}
		if p.IsDefault == 1 {
			cp := *p
			return &cp
		}
		if fallback == nil {
			cp := *p
			fallback = &cp
		}
	}
	return fallback
}

func (m *MemStore) findProviderForModel(model string) *Provider {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	lower := strings.ToLower(model)
	for _, p := range m.providers {
		if p.Status == 0 {
			continue
		}
		for _, mname := range parseProviderModels(p.Models) {
			if strings.EqualFold(mname, model) || strings.ToLower(mname) == lower {
				cp := *p
				return &cp
			}
		}
	}
	return nil
}

func (m *MemStore) listProfiles() []RouteProfile {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]RouteProfile, 0, len(m.profiles))
	for _, p := range m.profiles {
		out = append(out, *p)
	}
	return out
}

func (m *MemStore) createProfile(name, purpose string, models []string) RouteProfile {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := &RouteProfile{ID: newID("rp"), Name: name, Purpose: purpose, Models: models}
	m.profiles[p.ID] = p
	return *p
}

func (m *MemStore) findProfileByName(name string) *RouteProfile {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.profiles {
		if p.Name == name {
			cp := *p
			return &cp
		}
	}
	return nil
}

func (m *MemStore) listLogs(limit, offset int) []RequestLog {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= len(m.logs) {
		return nil
	}
	end := offset + limit
	if end > len(m.logs) {
		end = len(m.logs)
	}
	out := make([]RequestLog, 0, end-offset)
	for _, l := range m.logs[offset:end] {
		out = append(out, *l)
	}
	return out
}

func (m *MemStore) getLog(id string) (*RequestLog, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, l := range m.logs {
		if l.ID == id {
			cp := *l
			return &cp, true
		}
	}
	return nil, false
}

func (m *MemStore) listTasks() []AgentTask {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]AgentTask, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, *t)
	}
	return out
}

func (m *MemStore) createTask(t AgentTask) AgentTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	t.ID = newID("task")
	t.Status = "queued"
	t.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	if t.ToolSteps == nil {
		t.ToolSteps = []ToolStep{}
	}
	cp := t
	m.tasks[t.ID] = &cp
	return t
}

func (m *MemStore) updateTask(id string, fn func(*AgentTask)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tasks[id]; ok {
		fn(t)
	}
}

func (m *MemStore) listTags() []Tag {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Tag, 0, len(m.tags))
	for _, t := range m.tags {
		out = append(out, *t)
	}
	return out
}

func (m *MemStore) getTag(slug string) (*Tag, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tags[slug]
	if !ok {
		return nil, false
	}
	cp := *t
	return &cp, true
}

func (m *MemStore) ensureDemoTag() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.tags) > 0 {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	m.tags["general"] = &Tag{
		ID: newID("tag"), Slug: "general", Name: "General", Kind: "topic",
		UseCount: 0, UpdatedAt: now,
	}
}

func (m *MemStore) activeProviderCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, p := range m.providers {
		if p.Status == 1 {
			n++
		}
	}
	return n
}
