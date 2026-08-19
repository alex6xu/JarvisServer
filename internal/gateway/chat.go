package gateway

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/cli/run"
	"github.com/alex6xu/jarvisserver/internal/cli/ui"
	"github.com/alex6xu/jarvisserver/internal/plugin"
	"github.com/alex6xu/jarvisserver/internal/provider"
	"github.com/alex6xu/jarvisserver/internal/runtime"
	"github.com/alex6xu/jarvisserver/internal/session"
	"github.com/alex6xu/jarvisserver/internal/trust"
)

// Service owns shared gateway state and starts agent runs.
type Service struct {
	Opts    Options
	Runs    *RunManager
	Store   SessionRepository
	Control ControlRepository
	Audit   *GatewayStore
	Router  *ProviderRouter
	Trust   *trust.Manager
	Mem     *MemStore
}

// NewService constructs a Service with session store and trust manager.
func NewService(opts Options) (*Service, error) {
	opts = opts.withDefaults()
	if opts.Cwd == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		opts.Cwd = cwd
	}
	stateRoot := filepath.Join(opts.Cwd, ".jarvis")
	legacyStore, err := session.NewStore(filepath.Join(stateRoot, "sessions"))
	if err != nil {
		return nil, fmt.Errorf("session store: %w", err)
	}
	mgr, err := trust.NewManager(filepath.Join(stateRoot, "trust.json"))
	if err != nil {
		return nil, fmt.Errorf("trust manager: %w", err)
	}
	if opts.Approve {
		mgr.SetSessionTrust(opts.Cwd)
	}
	dbPath := opts.DatabasePath
	if dbPath == "" {
		dbPath = filepath.Join(opts.Cwd, ".jarvis", "gateway.db")
	}
	audit, err := OpenGatewayStore(dbPath)
	if err != nil {
		return nil, fmt.Errorf("gateway store: %w", err)
	}
	audit.maxAuditBodyBytes = opts.AuditMaxBodyBytes
	if err := importLegacySessions(legacyStore, audit); err != nil {
		_ = audit.Close()
		return nil, fmt.Errorf("import legacy sessions: %w", err)
	}
	if opts.AuditRetentionDays > 0 {
		before := time.Now().AddDate(0, 0, -opts.AuditRetentionDays)
		if err := audit.PruneAudit(context.Background(), before); err != nil {
			_ = audit.Close()
			return nil, fmt.Errorf("prune audit records: %w", err)
		}
	}
	adminPassword := opts.AdminPassword
	if adminPassword == "" {
		adminPassword = os.Getenv("JARVIS_ADMIN_PASSWORD")
	}
	count, err := audit.AccountCount(context.Background())
	if err != nil {
		_ = audit.Close()
		return nil, fmt.Errorf("count gateway accounts: %w", err)
	}
	if count == 0 {
		generated := false
		if adminPassword == "" {
			adminPassword, err = randomToken("")
			if err != nil {
				_ = audit.Close()
				return nil, fmt.Errorf("generate admin password: %w", err)
			}
			generated = true
		}
		if _, err := audit.CreateAccount(context.Background(), "dev", "dev@localhost", "admin", adminPassword); err != nil {
			_ = audit.Close()
			return nil, fmt.Errorf("bootstrap admin account: %w", err)
		}
		if generated {
			fmt.Fprintf(os.Stderr, "gateway: generated initial admin password for dev: %s\n", adminPassword)
		}
	}
	mem := newMemStore(opts.Model)
	storedProviders, err := audit.ListProviders(context.Background())
	if err != nil {
		_ = audit.Close()
		return nil, fmt.Errorf("load providers: %w", err)
	}
	if len(storedProviders) == 0 {
		legacyProviders := mem.listProviders()
		if len(legacyProviders) > 0 {
			if err := audit.ReplaceProviders(context.Background(), legacyProviders); err != nil {
				_ = audit.Close()
				return nil, fmt.Errorf("import legacy providers: %w", err)
			}
			storedProviders = legacyProviders
		}
	}
	mem.replaceProviders(storedProviders)
	if err := audit.ReplaceProviders(context.Background(), storedProviders); err != nil {
		_ = audit.Close()
		return nil, fmt.Errorf("synchronize provider endpoints: %w", err)
	}
	if err := initializeControlPlane(context.Background(), audit, mem, opts.Model); err != nil {
		_ = audit.Close()
		return nil, err
	}
	policy, err := ensureDefaultRoutePolicy(context.Background(), audit)
	if err != nil {
		_ = audit.Close()
		return nil, fmt.Errorf("initialize route policy: %w", err)
	}
	routerEngine, err := NewPersistentProviderRouter(audit, policy)
	if err != nil {
		_ = audit.Close()
		return nil, fmt.Errorf("initialize provider router: %w", err)
	}
	return &Service{
		Opts:    opts,
		Runs:    NewRunManager(audit),
		Store:   audit,
		Control: audit,
		Audit:   audit,
		Router:  routerEngine,
		Trust:   mgr,
		Mem:     mem,
	}, nil
}

// Close releases persistent Gateway resources.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	return s.Audit.Close()
}

// StartChat begins an async agent run and returns session/run ids immediately.
func (s *Service) StartChat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		return ChatResponse{}, fmt.Errorf("message is required")
	}
	model := strings.TrimSpace(req.Model)
	plan, err := s.resolveLLMPlan(model)
	if err != nil {
		return ChatResponse{}, err
	}
	route := plan.Candidates[0]
	model = route.Model

	runCwd := s.Opts.Cwd
	if req.WorkspaceID != "" {
		runCwd, err = s.workspaceDir(req.WorkspaceID)
		if err != nil {
			return ChatResponse{}, err
		}
		if info, statErr := os.Stat(runCwd); statErr != nil || !info.IsDir() {
			return ChatResponse{}, fmt.Errorf("workspace not found")
		}
	}

	content, err := ui.BuildUserContent(msg)
	if err != nil {
		return ChatResponse{}, err
	}

	prior, hs, err := s.openSession(req.SessionID, model, runCwd)
	if err != nil {
		return ChatResponse{}, err
	}

	noTools := s.Opts.NoTools || (strings.EqualFold(req.Mode, "coder") && req.WorkspaceID == "")
	env, err := run.SetupEnvAt(
		runCwd,
		model,
		route.BaseURL,
		route.Protocol,
		route.ProviderName,
		route.APIKey,
		noTools,
		s.Opts.NoSkills,
		runtime.BaseInstructionForMode(req.Mode),
		nil,
		true,
		run.ToolPolicy{},
	)
	if err != nil {
		return ChatResponse{}, err
	}

	sysPrompt := hs.header.SystemPrompt
	if sysPrompt == "" {
		sysPrompt = env.SysPrompt
		hs.header.SystemPrompt = sysPrompt
	}

	messages := append(agentcore.MessageList{}, prior...)
	messages = append(messages, agentcore.UserMessage{
		RoleField: agentcore.RoleUser,
		Content:   content,
	})
	agentCtx := &agentcore.AgentContext{
		SystemPrompt: sysPrompt,
		Messages:     messages,
		Tools:        env.Tools,
	}

	thinking, err := run.ResolveThinkingLevel(s.Opts.ThinkingLevel)
	if err != nil {
		closeEnv(env)
		return ChatResponse{}, err
	}

	source := "startup"
	if req.SessionID != "" {
		source = "resume"
	}
	set, herr := run.ResolveHookSet(env.Cwd, run.Trusted(env.Cwd))
	if herr != nil {
		closeEnv(env)
		return ChatResponse{}, herr
	}
	hookDeps := run.HookDeps{SessionID: hs.header.ID, ProjectDir: env.Cwd, WarnLog: os.Stderr}
	var baseOnEvent func(agentcore.AgentEvent)
	if n := plugin.NewEventNotifier(env.Plugins, os.Stderr); n != nil {
		baseOnEvent = n.Handle
	}

	runCtx, cancel := context.WithCancel(context.Background())
	state, err := s.Runs.Register(hs.header.ID, model, req.WorkspaceID, cancel)
	if err != nil {
		cancel()
		closeEnv(env)
		return ChatResponse{}, err
	}
	if err := s.Audit.CreateChat(context.Background(), ChatExchange{
		ID: newID("chat"), RunID: state.ID, SessionID: hs.header.ID,
		WorkspaceID: req.WorkspaceID, Mode: req.Mode, Model: model,
		RequestText: msg, CreatedAt: time.Now().UTC(),
	}); err != nil {
		cancel()
		state.Finish(err)
		closeEnv(env)
		return ChatResponse{}, fmt.Errorf("record chat request: %w", err)
	}

	routedProvider, err := s.buildRoutedProvider(plan, env.Provider, state.ID, hs.header.ID)
	if err != nil {
		cancel()
		state.Finish(err)
		closeEnv(env)
		_ = s.Audit.FinishChat(context.Background(), state.ID, "", runStatusError, err.Error(), time.Now().UTC())
		return ChatResponse{}, err
	}
	creds := provider.NewCredentialStore(nil)
	if route.APIKey != "" {
		creds.SetOverride(env.ProviderName, route.APIKey)
	}
	runCfg := run.NewConfig(model, env.ProviderName, thinking, routedProvider, creds, run.ToolRegistry(env.Tools), run.TodoReminders(env.Tools))
	runCfg.SessionID = hs.header.ID
	runCfg.MemoryRoot = run.MemoryRootFromTools(env.Tools)

	_, onEvent := run.InstallDriverHooks(runCtx, &runCfg, set, hookDeps, source, baseOnEvent)
	pub := func(ev StreamEvent) { state.Publish(ev) }
	handler, finish := NewTranslateHandler(pub, model, hs.header.ID, onEvent)

	go func() {
		defer cancel()
		defer closeEnv(env)

		stream := runtime.StartRun(runCtx, agentCtx, runCfg)
		_, drainErr := runtime.DrainStream(runCtx, stream, handler)
		finish(drainErr)
		if perr := persistSession(hs, agentCtx, model, env.ProviderName); perr != nil {
			fmt.Fprintf(os.Stderr, "gateway: persist session %s: %v\n", hs.header.ID, perr)
		}
		status, errorText := runStatusDone, ""
		responseText, stopReason, responseError := finalAssistantResult(agentCtx.Messages, hs.persisted)
		if drainErr != nil {
			status = runStatusError
			errorText = drainErr.Error()
			if contextCanceled(drainErr) {
				status = runStatusCancelled
			}
		} else if stopReason == agentcore.StopReasonError || stopReason == agentcore.StopReasonAborted {
			status = runStatusError
			errorText = responseError
			if stopReason == agentcore.StopReasonAborted {
				status = runStatusCancelled
			}
		}
		if err := s.Audit.FinishChat(context.Background(), state.ID, responseText, status, errorText, time.Now().UTC()); err != nil {
			fmt.Fprintf(os.Stderr, "gateway: finish chat audit %s: %v\n", state.ID, err)
		}
		state.Finish(drainErr)
	}()

	return ChatResponse{
		SessionID: hs.header.ID,
		RunID:     state.ID,
		Model:     model,
	}, nil
}

func (s *Service) buildRoutedProvider(plan RoutePlan, primary provider.Provider, runID, sessionID string) (*failoverProvider, error) {
	candidates := make([]routedCandidate, 0, len(plan.Candidates))
	for i, route := range plan.Candidates {
		candidateProvider := primary
		if i > 0 {
			resolved, _, err := provider.ResolveProvider(route.Model, route.BaseURL, route.Protocol, route.ProviderName, os.Getenv)
			if err != nil {
				s.Router.Observe(route.ProviderID, err)
				continue
			}
			candidateProvider = resolved
		}
		label := route.ProviderLabel
		if label == "" {
			label = candidateProvider.Name()
		}
		recorder := &recordingProvider{
			inner: candidateProvider, store: s.Audit, runID: runID, sessionID: sessionID,
			providerID: route.ProviderID, providerName: label,
		}
		candidates = append(candidates, routedCandidate{route: route, provider: recorder})
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("provider router: no candidates could be initialized")
	}
	return &failoverProvider{candidates: candidates, router: s.Router}, nil
}

func finalAssistantResult(messages agentcore.MessageList, start int) (text, stopReason, errorMessage string) {
	if start < 0 {
		start = 0
	}
	for i := len(messages) - 1; i >= start; i-- {
		if msg, ok := messages[i].(agentcore.AssistantMessage); ok {
			return agentcore.ContentToText(msg.Content), msg.StopReason, msg.ErrorMessage
		}
	}
	return "", "", ""
}

func closeEnv(env run.Env) {
	if env.Plugins != nil {
		_ = env.Plugins.Close()
	}
	if env.Memory != nil {
		_ = env.Memory.Close()
	}
}

type sessionHandle struct {
	store     SessionRepository
	header    session.SessionHeader
	curLeaf   string
	persisted int
}

func (s *Service) openSession(resumeID, model, cwd string) (agentcore.MessageList, sessionHandle, error) {
	now := time.Now().UTC()
	if resumeID != "" {
		h, entries, err := s.Store.LoadEntries(resumeID)
		if err != nil {
			return nil, sessionHandle{}, err
		}
		if h.Cwd != "" && filepath.Clean(h.Cwd) != filepath.Clean(cwd) {
			return nil, sessionHandle{}, fmt.Errorf("session workspace does not match requested workspace")
		}
		msgs := make(agentcore.MessageList, len(entries))
		for i, e := range entries {
			msgs[i] = e.Message
		}
		curLeaf := ""
		if len(entries) > 0 {
			curLeaf = entries[len(entries)-1].ID
		}
		return msgs, sessionHandle{
			store:     s.Store,
			header:    h,
			curLeaf:   curLeaf,
			persisted: len(msgs),
		}, nil
	}
	header := session.SessionHeader{
		ID:        session.NewID(now),
		CreatedAt: now,
		UpdatedAt: now,
		Model:     model,
		Cwd:       cwd,
	}
	return nil, sessionHandle{store: s.Store, header: header}, nil
}

func persistSession(hs sessionHandle, agentCtx *agentcore.AgentContext, model, providerName string) error {
	if hs.persisted > len(agentCtx.Messages) {
		hs.persisted = len(agentCtx.Messages)
	}
	tail := agentCtx.Messages[hs.persisted:]
	if len(tail) == 0 {
		return nil
	}
	hs.header.Model = model
	hs.header.Provider = providerName
	hs.header.UpdatedAt = time.Now().UTC()
	_, err := hs.store.AppendBranch(hs.header, hs.curLeaf, tail)
	return err
}
