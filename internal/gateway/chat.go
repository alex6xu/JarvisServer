package gateway

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/cli/headless"
	"github.com/smallnest/pigo/internal/cli/run"
	"github.com/smallnest/pigo/internal/cli/ui"
	"github.com/smallnest/pigo/internal/plugin"
	"github.com/smallnest/pigo/internal/provider"
	"github.com/smallnest/pigo/internal/runtime"
	"github.com/smallnest/pigo/internal/session"
	"github.com/smallnest/pigo/internal/trust"
)

// Service owns shared gateway state and starts agent runs.
type Service struct {
	Opts  Options
	Runs  *RunManager
	Store *session.Store
	Trust *trust.Manager
	Mem   *MemStore
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
	store, err := headless.SessionStore()
	if err != nil {
		return nil, fmt.Errorf("session store: %w", err)
	}
	mgr, err := trust.NewManager("")
	if err != nil {
		return nil, fmt.Errorf("trust manager: %w", err)
	}
	if opts.Approve {
		mgr.SetSessionTrust(opts.Cwd)
	}
	return &Service{
		Opts:  opts,
		Runs:  NewRunManager(),
		Store: store,
		Trust: mgr,
		Mem:   newMemStore(opts.Model),
	}, nil
}

// StartChat begins an async agent run and returns session/run ids immediately.
func (s *Service) StartChat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	_ = ctx
	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		return ChatResponse{}, fmt.Errorf("message is required")
	}
	model := strings.TrimSpace(req.Model)
	route, err := s.resolveLLM(model)
	if err != nil {
		return ChatResponse{}, err
	}
	model = route.Model

	content, err := ui.BuildUserContent(msg)
	if err != nil {
		return ChatResponse{}, err
	}

	prior, hs, err := s.openSession(req.SessionID, model)
	if err != nil {
		return ChatResponse{}, err
	}

	env, err := run.SetupEnv(
		model,
		route.BaseURL,
		route.Protocol,
		route.ProviderName,
		route.APIKey,
		s.Opts.NoTools,
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

	creds := provider.NewCredentialStore(nil)
	if route.APIKey != "" {
		creds.SetOverride(env.ProviderName, route.APIKey)
	}
	runCfg := run.NewConfig(model, env.ProviderName, thinking, env.Provider, creds, run.ToolRegistry(env.Tools), run.TodoReminders(env.Tools))
	runCfg.SessionID = hs.header.ID
	runCfg.MemoryRoot = run.MemoryRootFromTools(env.Tools)

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
	state := s.Runs.Register(hs.header.ID, model, req.WorkspaceID, cancel)

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
		state.Finish(drainErr)
	}()

	return ChatResponse{
		SessionID: hs.header.ID,
		RunID:     state.ID,
		Model:     model,
	}, nil
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
	store     *session.Store
	header    session.SessionHeader
	curLeaf   string
	persisted int
}

func (s *Service) openSession(resumeID, model string) (agentcore.MessageList, sessionHandle, error) {
	now := time.Now().UTC()
	if resumeID != "" {
		h, entries, err := s.Store.LoadEntries(resumeID)
		if err != nil {
			return nil, sessionHandle{}, err
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
		Cwd:       s.Opts.Cwd,
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
