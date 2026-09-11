package gateway

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/distributedlog"
)

const (
	queueEventEnqueue = "enqueue"
	queueEventPin     = "pin"
	queueEventSteer   = "steer"

	queueStatusPending   = "pending"
	queueStatusInjecting = "injecting"
	queueStatusInjected  = "injected"
	queueStatusExecuting = "executing"
	queueStatusCompleted = "completed"
	queueStatusAnswered  = "answered"
	queueStatusFailed    = "failed"
	queueStatusCancelled = "cancelled"
	queueStatusDropped   = "dropped"
)

var (
	ErrRunQueueClosed              = errors.New("run is finishing and no longer accepts queued messages")
	ErrRunQueueFull                = errors.New("run message queue is full")
	ErrRunQueueItemNotFound        = errors.New("queued message not found")
	ErrRunQueueItemNotPending      = errors.New("queued message is no longer pending")
	ErrRunQueueVersionConflict     = errors.New("queue version conflict")
	ErrRunQueueIdempotencyConflict = errors.New("idempotency key already used for another message")
)

type QueueMessageInput struct {
	AccountID      int
	Content        string
	EventType      string
	IdempotencyKey string
}

func normalizeQueueEventType(eventType string, pinned bool) (string, error) {
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	if eventType == "" {
		if pinned {
			return queueEventPin, nil
		}
		return queueEventEnqueue, nil
	}
	switch eventType {
	case queueEventEnqueue, queueEventPin, queueEventSteer:
		return eventType, nil
	default:
		return "", fmt.Errorf("unsupported queue event type %q", eventType)
	}
}

func (st *RunState) QueueMessage(input QueueMessageInput) (RunMessageQueueItem, RunMessageQueueSnapshot, error) {
	eventType, err := normalizeQueueEventType(input.EventType, false)
	if err != nil {
		return RunMessageQueueItem{}, RunMessageQueueSnapshot{}, err
	}
	content := strings.TrimSpace(input.Content)
	if content == "" {
		return RunMessageQueueItem{}, RunMessageQueueSnapshot{}, errors.New("message is required")
	}
	idempotencyKey := strings.TrimSpace(input.IdempotencyKey)
	if idempotencyKey == "" {
		idempotencyKey = newID("queue_req")
	}

	st.mu.Lock()
	for i := range st.queue.Items {
		existing := st.queue.Items[i]
		if existing.IdempotencyKey != idempotencyKey {
			continue
		}
		if existing.Content != content || existing.EventType != eventType {
			st.mu.Unlock()
			return RunMessageQueueItem{}, RunMessageQueueSnapshot{}, ErrRunQueueIdempotencyConflict
		}
		snapshot := cloneRunMessageQueueSnapshot(st.queue)
		st.mu.Unlock()
		return existing, snapshot, nil
	}
	if st.Status != runStatusRunning || !st.accepting {
		st.mu.Unlock()
		return RunMessageQueueItem{}, RunMessageQueueSnapshot{}, ErrRunQueueClosed
	}
	if pendingQueueItemCount(st.queue.Items) >= maxQueuedMessages {
		st.mu.Unlock()
		return RunMessageQueueItem{}, RunMessageQueueSnapshot{}, ErrRunQueueFull
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	item := RunMessageQueueItem{
		ID: newID("queue_msg"), SessionID: st.SessionID, RunID: st.ID,
		AccountID: input.AccountID, Content: content, EventType: eventType,
		Status: queueStatusPending, IdempotencyKey: idempotencyKey,
		CreatedAt: now, UpdatedAt: now,
	}
	previous := cloneRunMessageQueueSnapshot(st.queue)
	if eventType == queueEventPin {
		insertAt := len(st.queue.Items)
		for i := range st.queue.Items {
			candidate := st.queue.Items[i]
			if candidate.Status == queueStatusPending && candidate.EventType != queueEventPin {
				insertAt = i
				break
			}
		}
		st.queue.Items = slices.Insert(st.queue.Items, insertAt, item)
	} else {
		st.queue.Items = append(st.queue.Items, item)
	}
	st.bumpQueueLocked(now)
	if err := st.persistQueueLocked(); err != nil {
		st.queue = previous
		st.mu.Unlock()
		return RunMessageQueueItem{}, RunMessageQueueSnapshot{}, fmt.Errorf("persist run message queue: %w", err)
	}
	item = queueItemByID(st.queue.Items, item.ID)
	snapshot := cloneRunMessageQueueSnapshot(st.queue)
	st.mu.Unlock()

	st.publishQueueEvent("queue.updated", item, snapshot.Version)
	// Keep legacy clients observable while they migrate to normalized queue events.
	st.Publish(StreamEvent{Type: "user_queued", Content: item.Content, Pinned: item.EventType == queueEventPin,
		QueueItem: &item, QueueVersion: snapshot.Version})
	return item, snapshot, nil
}

// EnqueueMessage preserves the pre-queue API for internal callers and older tests.
func (st *RunState) EnqueueMessage(content string, _ agentcore.AgentMessage, pinned bool) error {
	eventType, _ := normalizeQueueEventType("", pinned)
	_, _, err := st.QueueMessage(QueueMessageInput{Content: content, EventType: eventType})
	return err
}

func (st *RunState) QueueSnapshot() RunMessageQueueSnapshot {
	st.mu.Lock()
	defer st.mu.Unlock()
	return cloneRunMessageQueueSnapshot(st.queue)
}

func (st *RunState) PinQueueMessage(messageID string) (RunMessageQueueSnapshot, error) {
	st.mu.Lock()
	index := queueItemIndex(st.queue.Items, messageID)
	if index < 0 {
		st.mu.Unlock()
		return RunMessageQueueSnapshot{}, ErrRunQueueItemNotFound
	}
	if st.Status != runStatusRunning || !st.accepting {
		st.mu.Unlock()
		return RunMessageQueueSnapshot{}, ErrRunQueueClosed
	}
	if st.queue.Items[index].Status != queueStatusPending {
		st.mu.Unlock()
		return RunMessageQueueSnapshot{}, ErrRunQueueItemNotPending
	}
	previous := cloneRunMessageQueueSnapshot(st.queue)
	item := st.queue.Items[index]
	st.queue.Items = slices.Delete(st.queue.Items, index, index+1)
	item.EventType = queueEventPin
	item.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	insertAt := len(st.queue.Items)
	for i := range st.queue.Items {
		candidate := st.queue.Items[i]
		if candidate.Status == queueStatusPending && candidate.EventType != queueEventPin {
			insertAt = i
			break
		}
	}
	st.queue.Items = slices.Insert(st.queue.Items, insertAt, item)
	st.bumpQueueLocked(item.UpdatedAt)
	if err := st.persistQueueLocked(); err != nil {
		st.queue = previous
		st.mu.Unlock()
		return RunMessageQueueSnapshot{}, fmt.Errorf("persist pinned message: %w", err)
	}
	item = queueItemByID(st.queue.Items, messageID)
	snapshot := cloneRunMessageQueueSnapshot(st.queue)
	st.mu.Unlock()
	st.publishQueueEvent("queue.updated", item, snapshot.Version)
	return snapshot, nil
}

func (st *RunState) ReorderQueueMessages(version int64, ids []string) (RunMessageQueueSnapshot, error) {
	st.mu.Lock()
	if st.Status != runStatusRunning || !st.accepting {
		st.mu.Unlock()
		return RunMessageQueueSnapshot{}, ErrRunQueueClosed
	}
	if version != st.queue.Version {
		st.mu.Unlock()
		return RunMessageQueueSnapshot{}, ErrRunQueueVersionConflict
	}
	pending := make(map[string]RunMessageQueueItem)
	for _, item := range st.queue.Items {
		if item.Status == queueStatusPending {
			pending[item.ID] = item
		}
	}
	if len(ids) != len(pending) {
		st.mu.Unlock()
		return RunMessageQueueSnapshot{}, errors.New("order must contain every pending message exactly once")
	}
	ordered := make([]RunMessageQueueItem, 0, len(ids))
	for _, id := range ids {
		item, ok := pending[id]
		if !ok {
			st.mu.Unlock()
			return RunMessageQueueSnapshot{}, errors.New("order contains an unknown or duplicate message")
		}
		ordered = append(ordered, item)
		delete(pending, id)
	}
	previous := cloneRunMessageQueueSnapshot(st.queue)
	nextPending := 0
	for i := range st.queue.Items {
		if st.queue.Items[i].Status == queueStatusPending {
			st.queue.Items[i] = ordered[nextPending]
			nextPending++
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	st.bumpQueueLocked(now)
	if err := st.persistQueueLocked(); err != nil {
		st.queue = previous
		st.mu.Unlock()
		return RunMessageQueueSnapshot{}, fmt.Errorf("persist queue order: %w", err)
	}
	snapshot := cloneRunMessageQueueSnapshot(st.queue)
	st.mu.Unlock()
	st.Publish(StreamEvent{Type: "queue.reordered", QueueVersion: snapshot.Version})
	return snapshot, nil
}

func (st *RunState) CancelQueueMessage(messageID string) (RunMessageQueueSnapshot, error) {
	st.mu.Lock()
	index := queueItemIndex(st.queue.Items, messageID)
	if index < 0 {
		st.mu.Unlock()
		return RunMessageQueueSnapshot{}, ErrRunQueueItemNotFound
	}
	if st.Status != runStatusRunning || !st.accepting {
		st.mu.Unlock()
		return RunMessageQueueSnapshot{}, ErrRunQueueClosed
	}
	if st.queue.Items[index].Status != queueStatusPending {
		st.mu.Unlock()
		return RunMessageQueueSnapshot{}, ErrRunQueueItemNotPending
	}
	previous := cloneRunMessageQueueSnapshot(st.queue)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	st.queue.Items[index].Status = queueStatusCancelled
	st.queue.Items[index].UpdatedAt = now
	st.bumpQueueLocked(now)
	if err := st.persistQueueLocked(); err != nil {
		st.queue = previous
		st.mu.Unlock()
		return RunMessageQueueSnapshot{}, fmt.Errorf("persist cancelled message: %w", err)
	}
	item := st.queue.Items[index]
	snapshot := cloneRunMessageQueueSnapshot(st.queue)
	st.mu.Unlock()
	st.publishQueueEvent("queue.cancelled", item, snapshot.Version)
	return snapshot, nil
}

func (st *RunState) DrainSteeringMessages() []agentcore.AgentMessage {
	return st.drainQueueMessages(func(item RunMessageQueueItem) bool {
		return item.Status == queueStatusPending && item.EventType == queueEventSteer
	}, false)
}

func (st *RunState) DrainFollowUpMessages() []agentcore.AgentMessage {
	return st.drainQueueMessages(func(item RunMessageQueueItem) bool {
		return item.Status == queueStatusPending
	}, false)
}

// DrainMessages is the legacy name for the follow-up boundary.
func (st *RunState) DrainMessages() []agentcore.AgentMessage {
	return st.DrainFollowUpMessages()
}

func (st *RunState) DrainFinalMessages() []agentcore.AgentMessage {
	return st.drainQueueMessages(func(item RunMessageQueueItem) bool {
		return item.Status == queueStatusPending
	}, true)
}

func (st *RunState) drainQueueMessages(selectItem func(RunMessageQueueItem) bool, sealWhenEmpty bool) []agentcore.AgentMessage {
	st.mu.Lock()
	selected := make([]RunMessageQueueItem, 0)
	for i := range st.queue.Items {
		if selectItem(st.queue.Items[i]) {
			selected = append(selected, st.queue.Items[i])
		}
	}
	if len(selected) == 0 {
		if sealWhenEmpty {
			st.accepting = false
		}
		st.mu.Unlock()
		return nil
	}
	previous := cloneRunMessageQueueSnapshot(st.queue)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	selectedIDs := make(map[string]bool, len(selected))
	for _, item := range selected {
		selectedIDs[item.ID] = true
	}
	for i := range st.queue.Items {
		if selectedIDs[st.queue.Items[i].ID] {
			st.queue.Items[i].Status = queueStatusInjecting
			st.queue.Items[i].UpdatedAt = now
		}
	}
	st.bumpQueueLocked(now)
	if err := st.persistQueueLocked(); err != nil {
		st.queue = previous
		st.mu.Unlock()
		st.logQueueError("persist injecting messages", err)
		return nil
	}
	injectingVersion := st.queue.Version
	selected = queueItemsByIDs(st.queue.Items, selectedIDs)
	st.mu.Unlock()
	for _, item := range selected {
		st.publishQueueEvent("queue.injecting", item, injectingVersion)
	}

	st.mu.Lock()
	previous = cloneRunMessageQueueSnapshot(st.queue)
	now = time.Now().UTC().Format(time.RFC3339Nano)
	for i := range st.queue.Items {
		if selectedIDs[st.queue.Items[i].ID] && st.queue.Items[i].Status == queueStatusInjecting {
			st.queue.Items[i].Status = queueStatusInjected
			st.queue.Items[i].UpdatedAt = now
		}
	}
	st.bumpQueueLocked(now)
	if err := st.persistQueueLocked(); err != nil {
		st.queue = previous
		st.mu.Unlock()
		st.logQueueError("persist injected messages", err)
		return nil
	}
	injectedVersion := st.queue.Version
	selected = queueItemsByIDs(st.queue.Items, selectedIDs)
	st.mu.Unlock()

	messages := make([]agentcore.AgentMessage, 0, len(selected))
	for _, item := range selected {
		messages = append(messages, queueAgentMessage(item))
		st.publishQueueEvent("queue.injected", item, injectedVersion)
		st.Publish(StreamEvent{Type: "user_injected", Content: item.Content,
			Pinned: item.EventType == queueEventPin, QueueItem: &item, QueueVersion: injectedVersion})
	}
	// One batch maps all messages drained at this boundary to the assistant turn
	// that follows. Clients use it to open exactly one response bubble even when
	// several queued messages are injected together.
	st.Publish(StreamEvent{Type: "queue.batch_injected", QueueItems: selected, QueueVersion: injectedVersion})
	st.transitionQueueItems(queueStatusExecuting, "queue.executing", func(item RunMessageQueueItem) bool {
		return selectedIDs[item.ID] && item.Status == queueStatusInjected
	})
	return messages
}

func queueAgentMessage(item RunMessageQueueItem) agentcore.AgentMessage {
	content := agentcore.ContentList{agentcore.NewTextContent(item.Content)}
	return queuedUserMessage(content, item.EventType == queueEventPin)
}

func (st *RunState) dropPendingQueueItems(eventType string) {
	st.transitionQueueItems(queueStatusDropped, eventType, func(item RunMessageQueueItem) bool {
		return item.Status == queueStatusPending || item.Status == queueStatusInjecting
	})
}

// FinishExecutingQueue marks the currently injected batch after its assistant
// turn settles. A natural response is answered; a terminal model response is
// failed. Tool-use turns are deliberately ignored because the batch is still
// being processed by the following turn.
func (st *RunState) FinishExecutingQueue(runErr error) {
	status, eventType := queueStatusAnswered, "queue.answered"
	if runErr != nil {
		status, eventType = queueStatusFailed, "queue.failed"
	}
	st.transitionQueueItems(status, eventType, func(item RunMessageQueueItem) bool {
		return item.Status == queueStatusExecuting
	})
}

func (st *RunState) finishQueue(runErr error) {
	status, eventType := queueStatusCompleted, "queue.completed"
	if runErr != nil {
		status, eventType = queueStatusFailed, "queue.failed"
	}
	st.transitionQueueItems(status, eventType, func(item RunMessageQueueItem) bool {
		return item.Status == queueStatusInjected || item.Status == queueStatusExecuting
	})
	st.dropPendingQueueItems("queue.dropped")
}

func (st *RunState) transitionQueueItems(status, eventType string, match func(RunMessageQueueItem) bool) {
	st.mu.Lock()
	var changed []RunMessageQueueItem
	now := time.Now().UTC().Format(time.RFC3339Nano)
	previous := cloneRunMessageQueueSnapshot(st.queue)
	for i := range st.queue.Items {
		if match(st.queue.Items[i]) {
			st.queue.Items[i].Status = status
			st.queue.Items[i].UpdatedAt = now
			changed = append(changed, st.queue.Items[i])
		}
	}
	if len(changed) == 0 {
		st.mu.Unlock()
		return
	}
	st.bumpQueueLocked(now)
	if err := st.persistQueueLocked(); err != nil {
		st.queue = previous
		st.mu.Unlock()
		st.logQueueError("persist queue transition", err)
		return
	}
	version := st.queue.Version
	st.mu.Unlock()
	for _, item := range changed {
		st.publishQueueEvent(eventType, item, version)
	}
}

func (st *RunState) bumpQueueLocked(_ string) {
	st.queue.RunID = st.ID
	st.queue.Version++
	for i := range st.queue.Items {
		st.queue.Items[i].Position = i
	}
}

func (st *RunState) persistQueueLocked() error {
	if st.store == nil {
		return nil
	}
	return st.store.SaveRunMessageQueue(context.Background(), st.queue)
}

func (st *RunState) publishQueueEvent(eventType string, item RunMessageQueueItem, version int64) {
	copy := item
	st.Publish(StreamEvent{Type: eventType, Content: item.Content,
		Pinned: item.EventType == queueEventPin, QueueItem: &copy, QueueVersion: version})
}

func (st *RunState) logQueueError(message string, err error) {
	if st.logger != nil {
		st.logger.Error(st.logContext(), message, distributedlog.Err(err))
	}
}

func cloneRunMessageQueueSnapshot(snapshot RunMessageQueueSnapshot) RunMessageQueueSnapshot {
	copy := snapshot
	copy.Items = append([]RunMessageQueueItem(nil), snapshot.Items...)
	return copy
}

func pendingQueueItemCount(items []RunMessageQueueItem) int {
	count := 0
	for _, item := range items {
		if item.Status == queueStatusPending || item.Status == queueStatusInjecting {
			count++
		}
	}
	return count
}

func queueItemIndex(items []RunMessageQueueItem, id string) int {
	for i := range items {
		if items[i].ID == id {
			return i
		}
	}
	return -1
}

func queueItemByID(items []RunMessageQueueItem, id string) RunMessageQueueItem {
	if index := queueItemIndex(items, id); index >= 0 {
		return items[index]
	}
	return RunMessageQueueItem{}
}

func queueItemsByIDs(items []RunMessageQueueItem, ids map[string]bool) []RunMessageQueueItem {
	out := make([]RunMessageQueueItem, 0, len(ids))
	for _, item := range items {
		if ids[item.ID] {
			out = append(out, item)
		}
	}
	return out
}
