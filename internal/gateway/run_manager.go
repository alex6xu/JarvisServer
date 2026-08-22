package gateway

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/distributedlog"
)

const (
	runStatusRunning     = "running"
	runStatusDone        = "done"
	runStatusError       = "error"
	runStatusCancelled   = "cancelled"
	runStatusTimedOut    = "timed_out"
	runStatusInterrupted = "interrupted"
	maxQueuedMessages    = 32
)

type queuedMessage struct {
	content string
	message agentcore.AgentMessage
}

// RunState holds one agent run's sequenced event log and live subscribers.
type RunState struct {
	ID          string
	SessionID   string
	Model       string
	WorkspaceID string
	Status      string
	Events      []StoredEvent
	LastSeq     int64
	Cancel      context.CancelFunc
	Deadline    time.Time
	Err         error
	queued      []queuedMessage
	accepting   bool

	mu     sync.Mutex
	subs   map[chan StoredEvent]struct{}
	done   chan struct{}
	store  *GatewayStore
	logger *distributedlog.Logger
	logCtx context.Context
}

// EnqueueMessage adds user guidance to a running agent. The runtime drains the
// queue after a tool batch or immediately before a natural run end.
func (st *RunState) EnqueueMessage(content string, message agentcore.AgentMessage) error {
	st.mu.Lock()
	if st.Status != runStatusRunning || !st.accepting {
		st.mu.Unlock()
		return fmt.Errorf("run is finishing and no longer accepts queued messages")
	}
	if len(st.queued) >= maxQueuedMessages {
		st.mu.Unlock()
		return fmt.Errorf("run message queue is full")
	}
	st.queued = append(st.queued, queuedMessage{content: content, message: message})
	st.mu.Unlock()
	st.Publish(StreamEvent{Type: "user_queued", Content: content})
	return nil
}

// DrainMessages atomically removes all queued guidance in submission order.
func (st *RunState) DrainMessages() []agentcore.AgentMessage {
	return st.drainMessages(false)
}

// DrainFinalMessages atomically seals an empty queue immediately before a
// natural run end, preventing a late accepted message from being lost.
func (st *RunState) DrainFinalMessages() []agentcore.AgentMessage {
	return st.drainMessages(true)
}

func (st *RunState) drainMessages(sealWhenEmpty bool) []agentcore.AgentMessage {
	st.mu.Lock()
	queued := append([]queuedMessage(nil), st.queued...)
	st.queued = nil
	if sealWhenEmpty && len(queued) == 0 {
		st.accepting = false
	}
	st.mu.Unlock()
	if len(queued) == 0 {
		return nil
	}
	messages := make([]agentcore.AgentMessage, 0, len(queued))
	for _, item := range queued {
		messages = append(messages, item.message)
		st.Publish(StreamEvent{Type: "user_injected", Content: item.content})
	}
	return messages
}

// RunManager tracks in-process runs for SSE subscription and after_seq replay.
type RunManager struct {
	mu     sync.Mutex
	runs   map[string]*RunState
	store  *GatewayStore
	logger *distributedlog.Logger
}

func NewRunManager(store ...*GatewayStore) *RunManager {
	return newRunManager(distributedlog.New(distributedlog.Config{}), store...)
}

func newRunManager(logger *distributedlog.Logger, store ...*GatewayStore) *RunManager {
	var persistent *GatewayStore
	if len(store) > 0 {
		persistent = store[0]
	}
	return &RunManager{runs: make(map[string]*RunState), store: persistent, logger: logger}
}

func newRunID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("run_%s_%s", time.Now().UTC().Format("20060102T150405"), hex.EncodeToString(b[:]))
}

// Register creates a running state and returns it. cancel cancels the run context.
func (m *RunManager) Register(sessionID, model, workspaceID string, cancel context.CancelFunc, deadline ...time.Time) (*RunState, error) {
	st := &RunState{
		ID:          newRunID(),
		SessionID:   sessionID,
		Model:       model,
		WorkspaceID: workspaceID,
		Status:      runStatusRunning,
		Cancel:      cancel,
		accepting:   true,
		subs:        make(map[chan StoredEvent]struct{}),
		done:        make(chan struct{}),
		store:       m.store,
		logger:      m.logger,
	}
	st.logCtx = distributedlog.WithRun(context.Background(), st.ID, st.SessionID)
	if len(deadline) > 0 {
		st.Deadline = deadline[0]
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, active := range m.runs {
		info := active.Info()
		if active.SessionID == sessionID && info.Status == runStatusRunning {
			return nil, fmt.Errorf("session already has an active run: %s", active.ID)
		}
	}
	if m.store != nil {
		if err := m.store.CreateRun(context.Background(), st); err != nil {
			return nil, fmt.Errorf("persist run: %w", err)
		}
	}
	m.runs[st.ID] = st
	return st, nil
}

// Cancel requests cancellation of a run. It is idempotent for terminal runs.
func (m *RunManager) Cancel(id string) bool {
	st, ok := m.Get(id)
	if !ok {
		return false
	}
	st.mu.Lock()
	if st.Status != runStatusRunning {
		st.mu.Unlock()
		return true
	}
	cancel := st.Cancel
	st.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return true
}

// Get returns a run by id.
func (m *RunManager) Get(id string) (*RunState, bool) {
	m.mu.Lock()
	st, ok := m.runs[id]
	m.mu.Unlock()
	if ok || m.store == nil {
		return st, ok
	}
	loaded, err := m.store.LoadRun(context.Background(), id)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			m.logger.Error(context.Background(), "load run failed",
				distributedlog.F("run_id", id), distributedlog.Err(err))
		}
		return nil, false
	}
	loaded.store = m.store
	loaded.logger = m.logger
	loaded.logCtx = distributedlog.WithRun(context.Background(), loaded.ID, loaded.SessionID)
	m.mu.Lock()
	if existing, exists := m.runs[id]; exists {
		loaded = existing
	} else {
		m.runs[id] = loaded
	}
	m.mu.Unlock()
	return loaded, true
}

// ActiveForSession returns the newest running run for a session, if any.
func (m *RunManager) ActiveForSession(sessionID string) *RunState {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best *RunState
	for _, st := range m.runs {
		info := st.Info()
		if st.SessionID != sessionID || info.Status != runStatusRunning {
			continue
		}
		if best == nil || st.ID > best.ID {
			best = st
		}
	}
	return best
}

func (m *RunManager) ActiveForWorkspace(workspaceID string) *RunState {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, st := range m.runs {
		info := st.Info()
		if st.WorkspaceID == workspaceID && info.Status == runStatusRunning {
			return st
		}
	}
	return nil
}

// Publish appends an event, assigns seq, and fans out to subscribers.
func (st *RunState) Publish(ev StreamEvent) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.LastSeq++
	ev.Seq = st.LastSeq
	if ev.SessionID == "" {
		ev.SessionID = st.SessionID
	}
	if ev.Model == "" {
		ev.Model = st.Model
	}
	if ev.Timestamp == "" {
		ev.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	stored := StoredEvent{Seq: st.LastSeq, Payload: ev}
	st.Events = append(st.Events, stored)
	if st.store != nil {
		if err := st.store.AppendRunEvent(context.Background(), st.ID, stored); err != nil {
			st.logger.Error(st.logContext(), "persist run event failed",
				distributedlog.F("event_seq", stored.Seq), distributedlog.Err(err))
		}
	}
	for ch := range st.subs {
		select {
		case ch <- stored:
		default:
			// Drop to a slow subscriber rather than block the agent loop.
			// Client can reconnect with after_seq to catch up from the log.
		}
	}
}

// Finish marks the run terminal and closes subscribers.
func (st *RunState) Finish(err error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	select {
	case <-st.done:
		return
	default:
		close(st.done)
	}
	st.Err = err
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			st.Status = runStatusTimedOut
		} else if contextCanceled(err) {
			st.Status = runStatusCancelled
		} else {
			st.Status = runStatusError
		}
	} else {
		st.Status = runStatusDone
	}
	if st.store != nil {
		errorText := ""
		if err != nil {
			errorText = err.Error()
		}
		if persistErr := st.store.FinishRun(context.Background(), st.ID, st.Status, errorText); persistErr != nil {
			st.logger.Error(st.logContext(), "finish run failed", distributedlog.Err(persistErr))
		}
	}
	for ch := range st.subs {
		close(ch)
		delete(st.subs, ch)
	}
}

func (st *RunState) logContext() context.Context {
	if st.logCtx != nil {
		return st.logCtx
	}
	return distributedlog.WithRun(context.Background(), st.ID, st.SessionID)
}

// Subscribe returns a channel of events with Seq > afterSeq, then live updates
// until the run finishes. The caller must drain until the channel is closed.
func (st *RunState) Subscribe(afterSeq int64) <-chan StoredEvent {
	return st.SubscribeContext(context.Background(), afterSeq)
}

// SubscribeContext is Subscribe with prompt cleanup when the client disconnects.
func (st *RunState) SubscribeContext(ctx context.Context, afterSeq int64) <-chan StoredEvent {
	st.mu.Lock()
	replay := make([]StoredEvent, 0)
	for _, ev := range st.Events {
		if ev.Seq > afterSeq {
			replay = append(replay, ev)
		}
	}
	running := st.Status == runStatusRunning
	var live chan StoredEvent
	if running {
		live = make(chan StoredEvent, 64)
		st.subs[live] = struct{}{}
	}
	st.mu.Unlock()

	out := make(chan StoredEvent, 64)
	go func() {
		defer close(out)
		if running {
			defer func() {
				st.mu.Lock()
				delete(st.subs, live)
				st.mu.Unlock()
			}()
		}
		send := func(event StoredEvent) bool {
			select {
			case out <- event:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for _, event := range replay {
			if !send(event) {
				return
			}
		}
		if !running {
			return
		}
		for {
			select {
			case event, open := <-live:
				if !open || !send(event) {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// WaitDone blocks until Finish.
func (st *RunState) WaitDone() {
	<-st.done
}

// Info builds the ActiveRunInfo snapshot.
func (st *RunState) Info() ActiveRunInfo {
	st.mu.Lock()
	defer st.mu.Unlock()
	info := ActiveRunInfo{
		ID:          st.ID,
		Status:      st.Status,
		LastSeq:     st.LastSeq,
		Model:       st.Model,
		WorkspaceID: st.WorkspaceID,
	}
	if !st.Deadline.IsZero() {
		info.DeadlineAt = st.Deadline.UTC().Format(time.RFC3339Nano)
	}
	return info
}
