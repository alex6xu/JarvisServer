package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

const (
	runStatusRunning   = "running"
	runStatusDone      = "done"
	runStatusError     = "error"
	runStatusCancelled = "cancelled"
)

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
	Err         error

	mu   sync.Mutex
	subs map[chan StoredEvent]struct{}
	done chan struct{}
}

// RunManager tracks in-process runs for SSE subscription and after_seq replay.
type RunManager struct {
	mu   sync.Mutex
	runs map[string]*RunState
}

func NewRunManager() *RunManager {
	return &RunManager{runs: make(map[string]*RunState)}
}

func newRunID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("run_%s_%s", time.Now().UTC().Format("20060102T150405"), hex.EncodeToString(b[:]))
}

// Register creates a running state and returns it. cancel cancels the run context.
func (m *RunManager) Register(sessionID, model, workspaceID string, cancel context.CancelFunc) *RunState {
	st := &RunState{
		ID:          newRunID(),
		SessionID:   sessionID,
		Model:       model,
		WorkspaceID: workspaceID,
		Status:      runStatusRunning,
		Cancel:      cancel,
		subs:        make(map[chan StoredEvent]struct{}),
		done:        make(chan struct{}),
	}
	m.mu.Lock()
	m.runs[st.ID] = st
	m.mu.Unlock()
	return st
}

// Get returns a run by id.
func (m *RunManager) Get(id string) (*RunState, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.runs[id]
	return st, ok
}

// ActiveForSession returns the newest running run for a session, if any.
func (m *RunManager) ActiveForSession(sessionID string) *RunState {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best *RunState
	for _, st := range m.runs {
		if st.SessionID != sessionID || st.Status != runStatusRunning {
			continue
		}
		if best == nil || st.ID > best.ID {
			best = st
		}
	}
	return best
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
	stored := StoredEvent{Seq: st.LastSeq, Payload: ev}
	st.Events = append(st.Events, stored)
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
		if contextCanceled(err) {
			st.Status = runStatusCancelled
		} else {
			st.Status = runStatusError
		}
	} else {
		st.Status = runStatusDone
	}
	for ch := range st.subs {
		close(ch)
		delete(st.subs, ch)
	}
}

// Subscribe returns a channel of events with Seq > afterSeq, then live updates
// until the run finishes. The caller must drain until the channel is closed.
func (st *RunState) Subscribe(afterSeq int64) <-chan StoredEvent {
	ch := make(chan StoredEvent, 64)
	st.mu.Lock()
	defer st.mu.Unlock()

	// Replay from the log first (synchronous send into buffer).
	for _, ev := range st.Events {
		if ev.Seq > afterSeq {
			ch <- ev
		}
	}
	if st.Status != runStatusRunning {
		close(ch)
		return ch
	}
	st.subs[ch] = struct{}{}
	return ch
}

// WaitDone blocks until Finish.
func (st *RunState) WaitDone() {
	<-st.done
}

// Info builds the ActiveRunInfo snapshot.
func (st *RunState) Info() ActiveRunInfo {
	st.mu.Lock()
	defer st.mu.Unlock()
	return ActiveRunInfo{
		ID:          st.ID,
		Status:      st.Status,
		LastSeq:     st.LastSeq,
		Model:       st.Model,
		WorkspaceID: st.WorkspaceID,
	}
}
