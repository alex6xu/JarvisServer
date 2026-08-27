package gateway

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/agenttool"
	"github.com/alex6xu/jarvisserver/internal/cli/run"
	"github.com/alex6xu/jarvisserver/internal/cli/ui"
	"github.com/alex6xu/jarvisserver/internal/compaction"
	"github.com/alex6xu/jarvisserver/internal/distributedlog"
	"github.com/alex6xu/jarvisserver/internal/plugin"
	"github.com/alex6xu/jarvisserver/internal/provider"
	"github.com/alex6xu/jarvisserver/internal/runtime"
	"github.com/alex6xu/jarvisserver/internal/session"
	"github.com/alex6xu/jarvisserver/internal/trust"
)

// Service owns shared gateway state and starts agent runs.
type Service struct {
	Opts           Options
	Runs           *RunManager
	Store          SessionRepository
	Control        ControlRepository
	Audit          *GatewayStore
	Router         *ProviderRouter
	Trust          *trust.Manager
	Mem            *MemStore
	GitHub         *GitHubService
	Stocks         *StockService
	Sentiment      *StockSentimentService
	NewsSentiment  *StockNewsSentimentService
	Crypto         *CryptoService
	Notifications  *NotificationService
	Digest         *StockDigestService
	Skills         *SkillRegistryService
	Logger         *distributedlog.Logger
	metadataCancel context.CancelFunc
	metadataDone   chan struct{}
	closeOnce      sync.Once
}

// gatewayToolPolicy keeps conversational runs read-only and lightweight. The
// memory tool is attached separately because its SQLite store is optional and
// a strict allow-list must not make chat startup fail when that store is down.
func gatewayToolPolicy(mode string, disabled bool) run.ToolPolicy {
	if disabled || strings.EqualFold(mode, "coder") {
		return run.ToolPolicy{}
	}
	return run.NewToolPolicy([]string{"websearch", "webfetch", "memory_search", "stock_latest_digest", "skill_load"}, nil)
}

func applyGatewayToolPolicy(mode string, disabled bool, tools []agentcore.AgentTool, snapshot SkillSnapshot) []agentcore.AgentTool {
	policy := gatewayToolPolicy(mode, disabled)
	if policy.IsZero() {
		return tools
	}
	restrictedBuiltins := map[string]bool{
		"read": true, "write": true, "edit": true, "grep": true, "find": true,
		"bash": true, "bash_output": true, "kill_bash": true, "todo": true, "task": true,
	}
	allowed := make(map[string]bool, len(policy.Allow))
	for _, name := range policy.Allow {
		allowed[name] = true
	}
	// An administrator-installed plugin may be enabled by a Skill. Built-in
	// workspace tools remain governed by the Chat profile and cannot be added by
	// Markdown frontmatter.
	for _, skill := range snapshot.Skills {
		for _, name := range skillAllowedTools(skill) {
			if !restrictedBuiltins[name] {
				allowed[name] = true
			}
		}
	}
	names := make([]string, 0, len(allowed))
	for name := range allowed {
		names = append(names, name)
	}
	return run.ApplyToolPolicy(tools, run.NewToolPolicy(names, nil))
}

func attachChatMemoryTool(mode string, env *run.Env) {
	if strings.EqualFold(mode, "coder") || env.Memory == nil {
		return
	}
	for _, tool := range env.Tools {
		if tool.Name() == "memory_search" {
			return
		}
	}
	env.Tools = append(env.Tools, &agenttool.MemorySearchTool{Store: env.Memory})
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
	if opts.DocumentsRoot == "" {
		opts.DocumentsRoot = filepath.Join(stateRoot, "documents")
	}
	if err := os.MkdirAll(opts.DocumentsRoot, 0o700); err != nil {
		return nil, fmt.Errorf("documents root: %w", err)
	}
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
	mem := newMemStore()
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
	if err := initializeControlPlane(context.Background(), audit, mem); err != nil {
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
	if recovered, err := audit.RecoverInterruptedRuns(context.Background()); err != nil {
		_ = audit.Close()
		return nil, fmt.Errorf("recover interrupted runs: %w", err)
	} else if recovered > 0 {
		opts.Logger.Info(context.Background(), "interrupted runs recovered",
			distributedlog.F("recovered_runs", recovered))
	}
	githubService, err := NewGitHubService(opts, audit, stateRoot)
	if err != nil {
		_ = audit.Close()
		return nil, fmt.Errorf("initialize github integration: %w", err)
	}
	notifications, err := NewNotificationService(opts, audit, stateRoot)
	if err != nil {
		_ = audit.Close()
		return nil, fmt.Errorf("initialize notifications: %w", err)
	}
	runs := newRunManager(opts.Logger, audit)
	service := &Service{
		Opts:          opts,
		Runs:          runs,
		Store:         audit,
		Control:       audit,
		Audit:         audit,
		Router:        routerEngine,
		Trust:         mgr,
		Mem:           mem,
		GitHub:        githubService,
		Stocks:        NewStockService(),
		Sentiment:     NewStockSentimentService(opts, audit),
		NewsSentiment: NewStockNewsSentimentService(opts, audit),
		Crypto:        NewCryptoService(opts),
		Notifications: notifications,
		Logger:        opts.Logger,
	}
	service.Digest = NewStockDigestService(service.Stocks, service.Crypto, service.NewsSentiment, service.Sentiment, service.Notifications, audit)
	if !opts.NoSkills {
		_, _ = run.LoadSkills(false)
		knownTools := []string{"stock_latest_digest", "skill_load", "memory_search"}
		for _, tool := range run.BuiltinTools(opts.Cwd, false) {
			knownTools = append(knownTools, tool.Name())
		}
		service.Skills = NewSkillRegistryService(audit, run.SkillsDir(), knownTools)
		if _, reloadErr := service.Skills.Reload(context.Background()); reloadErr != nil {
			opts.Logger.Error(context.Background(), "skill registry reload failed", distributedlog.Err(reloadErr))
		}
	}
	service.startProviderMetadataReconciler()
	return service, nil
}

// Close releases persistent Gateway resources.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	var closeErr error
	s.closeOnce.Do(func() {
		if s.metadataCancel != nil {
			s.metadataCancel()
		}
		if s.metadataDone != nil {
			<-s.metadataDone
		}
		closeErr = s.Audit.Close()
	})
	return closeErr
}

// StartChat begins an async agent run and returns session/run ids immediately.
func (s *Service) StartChat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		return ChatResponse{}, fmt.Errorf("message is required")
	}
	model := strings.TrimSpace(req.Model)
	var skillSnapshot SkillSnapshot
	if s.Skills != nil {
		var snapshotErr error
		skillSnapshot, snapshotErr = s.Skills.Snapshot(ctx, req.AccountID)
		if snapshotErr != nil {
			return ChatResponse{}, fmt.Errorf("load skills: %w", snapshotErr)
		}
		msg = expandGatewaySkillCommand(msg, skillSnapshot)
	}

	runCwd := s.Opts.Cwd
	var err error
	if req.WorkspaceID != "" {
		_, err = s.workspaceInfoForAccount(req.WorkspaceID, req.AccountID)
		if err != nil {
			return ChatResponse{}, err
		}
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
	messageDocuments, documentContext, err := s.prepareMessageDocuments(ctx, req)
	if err != nil {
		return ChatResponse{}, err
	}

	if req.SessionID != "" {
		if active := s.Runs.ActiveForSession(req.SessionID); active != nil {
			if len(messageDocuments.Documents) > 0 {
				return ChatResponse{}, errors.New("messages with documents cannot be queued while a run is active")
			}
			_, hs, _, err := s.openSession(req.SessionID, "", runCwd, req.WorkspaceID, sessionTypeForMode(req.Mode), req.AccountID)
			if err != nil {
				return ChatResponse{}, err
			}
			if active.WorkspaceID != req.WorkspaceID {
				return ChatResponse{}, fmt.Errorf("active run workspace does not match requested workspace")
			}
			if req.WorkspaceID != "" || sessionScopeMode(req.Mode) == "chat" {
				if err := s.setActiveSession(ctx, req.AccountID, sessionScopeMode(req.Mode), req.WorkspaceID, hs.header.ID); err != nil {
					return ChatResponse{}, fmt.Errorf("set active session: %w", err)
				}
			}
			eventType, err := normalizeQueueEventType(req.QueueEventType, req.Pinned)
			if err != nil {
				return ChatResponse{}, err
			}
			item, _, err := active.QueueMessage(QueueMessageInput{
				AccountID: req.AccountID, Content: msg, EventType: eventType,
				IdempotencyKey: req.IdempotencyKey,
			})
			if err != nil {
				return ChatResponse{}, err
			}
			return ChatResponse{
				SessionID: hs.header.ID,
				RunID:     active.ID,
				Model:     active.Model,
				Queued:    true,
				Pinned:    item.EventType == queueEventPin,
				QueueItem: &item,
			}, nil
		}
	}
	purpose := RoutePurposeChat
	if strings.EqualFold(req.Mode, "coder") {
		purpose = RoutePurposeCodeAnalysis
	}
	plan, err := s.resolveLLMPlanForPurpose(model, purpose, 0)
	if err != nil {
		return ChatResponse{}, err
	}
	route := plan.Candidates[0]
	model = route.Model

	prior, hs, runCwd, err := s.openSession(req.SessionID, model, runCwd, req.WorkspaceID, sessionTypeForMode(req.Mode), req.AccountID)
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
		true,
		runtime.BaseInstructionForMode(req.Mode),
		nil,
		true,
		run.ToolPolicy{},
	)
	if err != nil {
		return ChatResponse{}, err
	}
	attachChatMemoryTool(req.Mode, &env)
	if !noTools && s.Digest != nil {
		env.Tools = append(env.Tools, &StockDigestTool{Service: s.Digest, AccountID: req.AccountID, SessionID: hs.header.ID})
	}
	if !noTools && len(skillSnapshot.Skills) > 0 {
		env.Tools = append(env.Tools, &SkillLoadTool{Snapshot: skillSnapshot})
	}
	env.Tools = applyGatewayToolPolicy(req.Mode, noTools, env.Tools, skillSnapshot)

	sysPrompt := hs.header.SystemPrompt
	if sysPrompt == "" {
		sysPrompt = env.SysPrompt
	}
	sysPrompt = withGatewaySkillCatalog(sysPrompt, skillSnapshot)
	hs.header.SystemPrompt = sysPrompt

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

	runBaseCtx := distributedlog.WithRequestID(context.Background(), distributedlog.RequestID(ctx))
	var runCtx context.Context
	var cancel context.CancelFunc
	if s.Opts.RunTimeout > 0 {
		runCtx, cancel = context.WithTimeout(runBaseCtx, s.Opts.RunTimeout)
	} else {
		runCtx, cancel = context.WithCancel(runBaseCtx)
	}
	deadline, _ := runCtx.Deadline()
	if req.SessionID == "" {
		// Make a first-turn session resumable and queue-addressable while its run
		// is still active. The completed turn is appended by persistSession.
		if err := s.Store.SaveEntries(hs.header, nil); err != nil {
			cancel()
			closeEnv(env)
			return ChatResponse{}, fmt.Errorf("persist new session: %w", err)
		}
	}
	if req.WorkspaceID != "" || sessionScopeMode(req.Mode) == "chat" {
		if err := s.setActiveSession(ctx, req.AccountID, sessionScopeMode(req.Mode), req.WorkspaceID, hs.header.ID); err != nil {
			cancel()
			closeEnv(env)
			return ChatResponse{}, fmt.Errorf("set active session: %w", err)
		}
	}
	state, err := s.Runs.Register(hs.header.ID, model, req.WorkspaceID, cancel, deadline)
	if err != nil {
		cancel()
		closeEnv(env)
		return ChatResponse{}, err
	}
	runCtx = distributedlog.WithRun(runCtx, state.ID, hs.header.ID)
	state.logCtx = runCtx
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
	liveSession, err := newLiveSessionWriter(hs, model, env.ProviderName, state.ID, s.Audit)
	if err != nil {
		cancel()
		state.Finish(err)
		closeEnv(env)
		_ = s.Audit.FinishChat(context.Background(), state.ID, "", runStatusError, err.Error(), time.Now().UTC())
		return ChatResponse{}, fmt.Errorf("initialize realtime session persistence: %w", err)
	}
	if err := liveSession.PersistInitialWithDocuments(messages[hs.persisted], messageDocuments); err != nil {
		cancel()
		state.Finish(err)
		closeEnv(env)
		_ = s.Audit.FinishChat(context.Background(), state.ID, "", runStatusError, err.Error(), time.Now().UTC())
		return ChatResponse{}, fmt.Errorf("persist user message: %w", err)
	}

	routedProvider, err := s.buildRoutedProvider(req.Model, plan, state.ID, hs.header.ID, req.WorkspaceID, req.Mode, state.Publish)
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
	if documentContext != "" {
		targetIndex := hs.persisted
		runCfg.TransformContext = func(_ context.Context, input agentcore.MessageList) agentcore.MessageList {
			if targetIndex < 0 || targetIndex >= len(input) {
				return input
			}
			user, ok := input[targetIndex].(agentcore.UserMessage)
			if !ok {
				return input
			}
			// Transform only a copy used for the provider request. The durable Agent
			// context and chat audit retain the user's original question.
			output := append(agentcore.MessageList(nil), input...)
			user.Content = append(append(agentcore.ContentList(nil), user.Content...), agentcore.NewTextContent(documentContext))
			output[targetIndex] = user
			return output
		}
	}
	baseSteering := runCfg.GetSteeringMessages
	runCfg.GetSteeringMessages = func(ctx context.Context) []agentcore.AgentMessage {
		var messages []agentcore.AgentMessage
		if baseSteering != nil {
			messages = append(messages, baseSteering(ctx)...)
		}
		messages = append(messages, state.DrainSteeringMessages()...)
		liveSession.QueueMessages(messages)
		return messages
	}
	baseFollowUp := runCfg.GetFollowUpMessages
	runCfg.GetFollowUpMessages = func(ctx context.Context, agentCtx *agentcore.AgentContext) []agentcore.AgentMessage {
		var messages []agentcore.AgentMessage
		if baseFollowUp != nil {
			messages = append(messages, baseFollowUp(ctx, agentCtx)...)
		}
		messages = append(messages, state.DrainFollowUpMessages()...)
		liveSession.QueueMessages(messages)
		return messages
	}
	runCfg.GetFinalMessages = func(context.Context, *agentcore.AgentContext) []agentcore.AgentMessage {
		messages := state.DrainFinalMessages()
		liveSession.QueueMessages(messages)
		return messages
	}
	runCfg.ContextWindow = route.ContextWindow
	if runCfg.ContextWindow <= 0 {
		runCfg.ContextWindow = s.adaptiveContextWindow(req.Model, req.Mode)
	}
	runCfg.Compaction = smartCompactionSettings(runCfg.ContextWindow)
	var selectedContextWindow atomic.Int64
	selectedContextWindow.Store(int64(runCfg.ContextWindow))
	var appliedContextWindow atomic.Int64
	appliedContextWindow.Store(int64(runCfg.ContextWindow))
	routedProvider.onRouteSelected = func(selected LLMRoute) {
		if selected.ContextWindow > 0 {
			selectedContextWindow.Store(int64(selected.ContextWindow))
		}
	}
	basePrepareNextTurn := runCfg.PrepareNextTurn
	runCfg.PrepareNextTurn = func(ctx context.Context, agentCtx *agentcore.AgentContext) *runtime.TurnUpdate {
		var update *runtime.TurnUpdate
		if basePrepareNextTurn != nil {
			update = basePrepareNextTurn(ctx, agentCtx)
		}
		window := int(selectedContextWindow.Load())
		if window <= 0 || window == int(appliedContextWindow.Load()) {
			return update
		}
		if update == nil {
			update = &runtime.TurnUpdate{}
		}
		settings := smartCompactionSettings(window)
		update.ContextWindow = &window
		update.Compaction = &settings
		appliedContextWindow.Store(int64(window))
		return update
	}
	baseSummaryStream := runCfg.Stream
	runCfg.SummaryStream = func(ctx context.Context, model string, llm provider.LlmContext, cfg provider.StreamConfig) (*provider.AssistantMessageEventStream, error) {
		extra := make(map[string]any, len(cfg.Extra)+1)
		for key, value := range cfg.Extra {
			extra[key] = value
		}
		extra["route_purpose"] = string(RoutePurposeCompaction)
		cfg.Extra = extra
		return baseSummaryStream(ctx, model, llm, cfg)
	}

	_, onEvent := run.InstallDriverHooks(runCtx, &runCfg, set, hookDeps, source, baseOnEvent)
	pub := func(ev StreamEvent) { state.Publish(ev) }
	persistOnEvent := func(event agentcore.AgentEvent) {
		if turn, ok := event.(agentcore.TurnEndEvent); ok && len(turn.Message.ToolCalls()) == 0 {
			var turnErr error
			if turn.Message.StopReason == agentcore.StopReasonError || turn.Message.StopReason == agentcore.StopReasonAborted {
				turnErr = errors.New(strings.TrimSpace(turn.Message.ErrorMessage + " " + turn.Message.StopReason))
			}
			state.FinishExecutingQueue(turnErr)
		}
		if err := liveSession.HandleEvent(event); err != nil {
			s.Logger.Error(runCtx, "persist live session event failed",
				distributedlog.F("event_type", event.EventType()), distributedlog.Err(err))
		}
		if onEvent != nil {
			onEvent(event)
		}
	}
	handler, finish := NewTranslateHandler(pub, model, hs.header.ID, persistOnEvent)
	runStarted := time.Now()
	s.Logger.Info(runCtx, "agent run started",
		distributedlog.F("mode", req.Mode),
		distributedlog.F("model", model),
		distributedlog.F("workspace_id", req.WorkspaceID),
	)

	go func() {
		defer cancel()
		defer closeEnv(env)

		stream := runtime.StartRun(runCtx, agentCtx, runCfg)
		_, drainErr := runtime.DrainStream(runCtx, stream, handler)
		finish(drainErr)
		persisted := min(hs.persisted, len(agentCtx.Messages))
		if perr := liveSession.Finalize(agentCtx.Messages[persisted:]); perr != nil {
			s.Logger.Error(runCtx, "persist session failed", distributedlog.Err(perr))
		}
		status, errorText := runStatusDone, ""
		responseText, stopReason, responseError := finalAssistantResult(agentCtx.Messages, hs.persisted)
		if drainErr != nil {
			status = runStatusError
			errorText = drainErr.Error()
			if errors.Is(drainErr, context.DeadlineExceeded) {
				status = runStatusTimedOut
			} else if contextCanceled(drainErr) {
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
			s.Logger.Error(runCtx, "finish chat audit failed", distributedlog.Err(err))
		}
		state.Finish(drainErr)
		if s.Notifications != nil {
			go s.Notifications.NotifyRun(context.Background(), req.AccountID, RunNotification{
				RunID: state.ID, Status: status, Mode: req.Mode, Model: model, SessionID: hs.header.ID,
				WorkspaceID: req.WorkspaceID, Duration: time.Since(runStarted), Response: responseText, Error: errorText,
			})
		}
		fields := []distributedlog.Field{
			distributedlog.F("mode", req.Mode),
			distributedlog.F("model", model),
			distributedlog.F("workspace_id", req.WorkspaceID),
			distributedlog.F("status", status),
			distributedlog.F("duration_ms", time.Since(runStarted).Milliseconds()),
		}
		if errorText != "" {
			fields = append(fields, distributedlog.F("error", errorText))
		}
		if status == runStatusError || status == runStatusTimedOut {
			s.Logger.Error(runCtx, "agent run completed", fields...)
		} else {
			s.Logger.Info(runCtx, "agent run completed", fields...)
		}
	}()

	return ChatResponse{
		SessionID: hs.header.ID,
		RunID:     state.ID,
		Model:     model,
	}, nil
}

const pinnedGuidancePreamble = `<system-reminder>
The user pinned the guidance below during the active run. Treat it as the highest-priority current user intent and re-plan the remaining work around it. Do not claim completion until its requested outcome or termination condition is satisfied. If it defines a stop condition, stop when that condition is met. It supersedes conflicting earlier user guidance, but never system or developer instructions.
</system-reminder>`

func queuedUserMessage(content agentcore.ContentList, pinned bool) agentcore.UserMessage {
	if pinned {
		prioritized := make(agentcore.ContentList, 0, len(content)+1)
		prioritized = append(prioritized, agentcore.NewTextContent(pinnedGuidancePreamble))
		content = append(prioritized, content...)
	}
	return agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: content}
}

func (s *Service) instantiateRoutePlan(plan RoutePlan, runID, sessionID string) ([]routedCandidate, error) {
	candidates := make([]routedCandidate, 0, len(plan.Candidates))
	for _, route := range plan.Candidates {
		candidateProvider, _, err := provider.ResolveProvider(route.Model, route.BaseURL, route.Protocol, route.ProviderName, os.Getenv)
		if err != nil {
			s.Router.Observe(route.ProviderID, err)
			continue
		}
		label := route.ProviderLabel
		if label == "" {
			label = candidateProvider.Name()
		}
		recorder := &recordingProvider{
			inner: candidateProvider, store: s.Audit, runID: runID, sessionID: sessionID,
			providerID: route.ProviderID, providerName: label, logger: s.Logger,
		}
		candidates = append(candidates, routedCandidate{route: route, provider: recorder})
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("provider router: no candidates could be initialized")
	}
	return candidates, nil
}

func (s *Service) buildRoutedProvider(requestedModel string, initial RoutePlan, runID, sessionID, workspaceID, mode string, publish func(StreamEvent)) (*failoverProvider, error) {
	initialCandidates, err := s.instantiateRoutePlan(initial, runID, sessionID)
	if err != nil {
		return nil, err
	}
	return &failoverProvider{
		candidates: initialCandidates, router: s.Router, store: s.Audit, runID: runID,
		sessionID: sessionID, workspaceID: workspaceID, mode: mode, publish: publish,
		planner: func(ctx context.Context, purpose RoutePurpose, req provider.CompletionRequest) (RoutePlan, []routedCandidate, error) {
			model := requestedModel
			minContextWindow := compaction.EstimateContextTokensWithPrompt(
				req.Context.SystemPrompt, req.Context.Messages, req.Context.Tools).Tokens + 2048
			if purpose == RoutePurposeCompaction {
				model = ""
			}
			plan, err := s.resolveLLMPlanForPurpose(model, purpose, minContextWindow)
			if err != nil {
				return RoutePlan{}, nil, err
			}
			candidates, err := s.instantiateRoutePlan(plan, runID, sessionID)
			return plan, candidates, err
		},
	}, nil
}

func (s *Service) adaptiveContextWindow(requestedModel, mode string) int {
	model := strings.TrimSpace(requestedModel)
	if strings.EqualFold(model, "auto") {
		model = ""
	}
	minimum := 0
	for _, raw := range s.Mem.listProviders() {
		provider := normalizeProviderConfig(raw)
		if !providerUsable(&provider) {
			continue
		}
		if model != "" {
			matched := false
			for _, candidate := range parseProviderModels(provider.Models) {
				if strings.EqualFold(candidate, model) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		eligible := provider.Capabilities.Chat && provider.QualityTier >= 1
		if strings.EqualFold(mode, "coder") {
			eligible = provider.Capabilities.Tools &&
				((provider.Capabilities.Reasoning && provider.QualityTier >= 3) ||
					(provider.Capabilities.Coding && provider.QualityTier >= 2))
		}
		if eligible {
			for _, modelID := range parseProviderModels(provider.Models) {
				if model != "" && !strings.EqualFold(modelID, model) {
					continue
				}
				window := providerModelMetadataFor(provider, modelID).EffectiveContextWindow
				if window > 0 && (minimum == 0 || window < minimum) {
					minimum = window
				}
			}
		}
	}
	if minimum <= 0 {
		minimum = 32768
	}
	return minimum
}

func smartCompactionSettings(contextWindow int) compaction.CompactionSettings {
	if contextWindow <= 0 {
		return compaction.CompactionSettings{}
	}
	reserve := contextWindow / 8
	if reserve < 2048 {
		reserve = 2048
	}
	if reserve > 16384 {
		reserve = 16384
	}
	keepRecent := contextWindow / 3
	if keepRecent < 4096 {
		keepRecent = 4096
	}
	if keepRecent > 20000 {
		keepRecent = 20000
	}
	if reserve+keepRecent >= contextWindow {
		keepRecent = max((contextWindow-reserve)/2, 512)
	}
	return compaction.CompactionSettings{Enabled: true, ReserveTokens: reserve, KeepRecentTokens: keepRecent}
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

func (s *Service) openSession(resumeID, model, cwd, workspaceID, requestedType string, accountID int) (agentcore.MessageList, sessionHandle, string, error) {
	now := time.Now().UTC()
	if resumeID != "" {
		h, entries, err := s.Store.LoadEntries(resumeID)
		if err != nil {
			return nil, sessionHandle{}, "", err
		}
		if !sessionOwnedByAccount(h, accountID) {
			return nil, sessionHandle{}, "", fmt.Errorf("session does not belong to this account")
		}
		if sessionTypeFromHeader(h) != requestedType {
			return nil, sessionHandle{}, "", fmt.Errorf("session type does not match requested page")
		}
		h.Type = requestedType
		if h.WorkspaceID != "" && h.WorkspaceID != workspaceID {
			return nil, sessionHandle{}, "", fmt.Errorf("session workspace does not match requested workspace")
		}
		executionCwd := cwd
		if h.Cwd != "" {
			if h.WorkspaceID == "" && filepath.Clean(h.Cwd) != filepath.Clean(cwd) {
				return nil, sessionHandle{}, "", fmt.Errorf("session workspace does not match requested workspace")
			}
			if h.WorktreeBranch != "" {
				root, rootErr := filepath.Abs(s.sessionWorktreesRoot())
				target, targetErr := filepath.Abs(h.Cwd)
				if rootErr != nil || targetErr != nil || ensurePathWithin(root, target) != nil {
					return nil, sessionHandle{}, "", fmt.Errorf("invalid session worktree")
				}
			}
			executionCwd = h.Cwd
		}
		if info, statErr := os.Stat(executionCwd); statErr != nil || !info.IsDir() {
			return nil, sessionHandle{}, "", fmt.Errorf("session working directory not found")
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
		}, executionCwd, nil
	}
	header := session.SessionHeader{
		ID:          session.NewID(now),
		CreatedAt:   now,
		UpdatedAt:   now,
		Model:       model,
		Cwd:         cwd,
		AccountID:   accountID,
		WorkspaceID: workspaceID,
		Type:        requestedType,
	}
	return nil, sessionHandle{store: s.Store, header: header}, cwd, nil
}
