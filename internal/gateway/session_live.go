package gateway

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/session"
)

const liveSessionFlushInterval = 250 * time.Millisecond

// liveSessionWriter keeps the durable session close to the event stream. User
// and tool messages are appended immediately; an in-progress assistant entry is
// updated at a bounded frequency and forced to disk at message boundaries.
type liveSessionWriter struct {
	mu       sync.Mutex
	store    SessionRepository
	realtime RealtimeSessionRepository
	audit    *GatewayStore
	runID    string
	header   session.SessionHeader
	parent   string
	written  []session.Entry
	pending  []agentcore.AgentMessage
	current  int
	interval time.Duration
	lastSave time.Time
	updates  int
}

func newLiveSessionWriter(hs sessionHandle, model, providerName, runID string, audit *GatewayStore) (*liveSessionWriter, error) {
	realtime, ok := hs.store.(RealtimeSessionRepository)
	if !ok {
		return nil, fmt.Errorf("session repository does not support realtime writes")
	}
	header := hs.header
	header.Model = model
	header.Provider = providerName
	return &liveSessionWriter{
		store: hs.store, realtime: realtime, audit: audit, runID: runID,
		header: header, parent: hs.curLeaf, current: -1, interval: liveSessionFlushInterval,
	}, nil
}

func (w *liveSessionWriter) PersistInitial(message agentcore.AgentMessage) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err := w.appendLocked(message)
	return err
}

// QueueMessages records user guidance at the next assistant-message boundary.
// The queue is filled by the runtime before it starts that assistant turn, so
// the durable order remains assistant -> tool results -> guidance -> assistant.
func (w *liveSessionWriter) QueueMessages(messages []agentcore.AgentMessage) {
	if len(messages) == 0 {
		return
	}
	w.mu.Lock()
	w.pending = append(w.pending, messages...)
	w.mu.Unlock()
}

func (w *liveSessionWriter) HandleEvent(event agentcore.AgentEvent) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch e := event.(type) {
	case agentcore.MessageStartEvent:
		if err := w.flushPendingLocked(); err != nil {
			return err
		}
		return w.startAssistantLocked(e.Message)
	case agentcore.MessageUpdateEvent:
		return w.updateAssistantLocked(e.Message, false)
	case agentcore.MessageEndEvent:
		return w.updateAssistantLocked(e.Message, true)
	case agentcore.TurnEndEvent:
		if err := w.updateAssistantLocked(e.Message, true); err != nil {
			return err
		}
		for _, result := range e.ToolResults {
			if _, err := w.appendLocked(result); err != nil {
				return err
			}
		}
		w.current = -1
		w.updates = 0
	}
	return nil
}

func (w *liveSessionWriter) startAssistantLocked(message agentcore.AgentMessage) error {
	if w.current >= 0 {
		return w.updateAssistantLocked(message, true)
	}
	entry, err := w.appendLocked(message)
	if err != nil {
		return err
	}
	w.current = len(w.written) - 1
	w.lastSave = time.Now()
	w.updates = 0
	return w.checkpointResponseLocked(entry.Message)
}

func (w *liveSessionWriter) updateAssistantLocked(message agentcore.AgentMessage, force bool) error {
	if w.current < 0 {
		return w.startAssistantLocked(message)
	}
	entry := &w.written[w.current]
	if reflect.DeepEqual(entry.Message, message) {
		return nil
	}
	if !force && w.updates > 0 && time.Since(w.lastSave) < w.interval {
		return nil
	}
	updated := *entry
	updated.Message = message
	w.header.UpdatedAt = time.Now().UTC()
	if err := w.realtime.UpdateSessionEntry(w.header, updated); err != nil {
		return err
	}
	*entry = updated
	w.lastSave = time.Now()
	w.updates++
	return w.checkpointResponseLocked(message)
}

func (w *liveSessionWriter) flushPendingLocked() error {
	for _, message := range w.pending {
		if _, err := w.appendLocked(message); err != nil {
			return err
		}
	}
	w.pending = nil
	return nil
}

func (w *liveSessionWriter) appendLocked(message agentcore.AgentMessage) (session.Entry, error) {
	w.header.UpdatedAt = time.Now().UTC()
	entry, err := w.realtime.AppendSessionEntry(w.header, w.parent, message)
	if err != nil {
		return session.Entry{}, err
	}
	w.parent = entry.ID
	w.written = append(w.written, entry)
	return entry, nil
}

func (w *liveSessionWriter) checkpointResponseLocked(message agentcore.Message) error {
	assistant, ok := message.(agentcore.AssistantMessage)
	if !ok || w.audit == nil {
		return nil
	}
	return w.audit.UpdateChatResponse(context.Background(), w.runID, agentcore.ContentToText(assistant.Content))
}

// Finalize reconciles the live entries with the authoritative AgentContext.
// The normal path reuses every live entry id; the rebuild also covers uncommon
// context mutations such as compaction or stop-hook guidance.
func (w *liveSessionWriter) Finalize(messages agentcore.MessageList) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.matchesLocked(messages) {
		w.pending = nil
		w.current = -1
		return nil
	}
	header, entries, err := w.store.LoadEntries(w.header.ID)
	if err != nil {
		return err
	}
	writtenByID := make(map[string]session.Entry, len(w.written))
	for _, entry := range w.written {
		writtenByID[entry.ID] = entry
	}
	kept := make([]session.Entry, 0, len(entries)+len(messages))
	for _, entry := range entries {
		if _, belongsToRun := writtenByID[entry.ID]; !belongsToRun {
			kept = append(kept, entry)
		}
	}
	now := time.Now().UTC()
	parent := w.parentBeforeRun()
	for i, message := range messages {
		entry := session.Entry{ID: newID("msg"), ParentID: parent, Timestamp: now, Message: message}
		if i < len(w.written) && w.written[i].Message.Role() == message.Role() {
			entry.ID = w.written[i].ID
			entry.Timestamp = w.written[i].Timestamp
		}
		kept = append(kept, entry)
		parent = entry.ID
	}
	header.Model = w.header.Model
	header.Provider = w.header.Provider
	header.UpdatedAt = now
	w.header = header
	w.parent = parent
	w.pending = nil
	w.current = -1
	return w.store.SaveEntries(header, kept)
}

func (w *liveSessionWriter) matchesLocked(messages agentcore.MessageList) bool {
	if len(w.pending) != 0 || len(w.written) != len(messages) {
		return false
	}
	for i, message := range messages {
		if !reflect.DeepEqual(w.written[i].Message, message) {
			return false
		}
	}
	return true
}

func (w *liveSessionWriter) parentBeforeRun() string {
	if len(w.written) == 0 {
		return w.parent
	}
	return w.written[0].ParentID
}
