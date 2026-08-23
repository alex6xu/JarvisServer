package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type queueMessageRequest struct {
	Content        string `json:"content"`
	EventType      string `json:"event_type"`
	IdempotencyKey string `json:"idempotency_key"`
}

type reorderQueueRequest struct {
	Version int64    `json:"version"`
	Order   []string `json:"order"`
}

func (s *Service) ownedRunFromRequest(w http.ResponseWriter, r *http.Request) (*RunState, int, bool) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return nil, 0, false
	}
	state, ok := s.Runs.Get(pathParam(r, "runId"))
	if !ok || !s.runOwnedByAccount(state, accountID) {
		writeErr(w, http.StatusNotFound, "run not found")
		return nil, 0, false
	}
	return state, accountID, true
}

func (s *Service) handleGetRunMessageQueue(w http.ResponseWriter, r *http.Request) {
	state, _, ok := s.ownedRunFromRequest(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, state.QueueSnapshot())
}

func (s *Service) handlePostRunMessageQueue(w http.ResponseWriter, r *http.Request) {
	state, accountID, ok := s.ownedRunFromRequest(w, r)
	if !ok {
		return
	}
	var body queueMessageRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	item, snapshot, err := state.QueueMessage(QueueMessageInput{
		AccountID: accountID, Content: body.Content, EventType: body.EventType,
		IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		writeRunQueueError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"item": item, "queue": snapshot})
}

func (s *Service) handlePinRunMessage(w http.ResponseWriter, r *http.Request) {
	state, _, ok := s.ownedRunFromRequest(w, r)
	if !ok {
		return
	}
	snapshot, err := state.PinQueueMessage(pathParam(r, "messageId"))
	if err != nil {
		writeRunQueueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Service) handleReorderRunMessageQueue(w http.ResponseWriter, r *http.Request) {
	state, _, ok := s.ownedRunFromRequest(w, r)
	if !ok {
		return
	}
	var body reorderQueueRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	snapshot, err := state.ReorderQueueMessages(body.Version, body.Order)
	if err != nil {
		writeRunQueueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Service) handleDeleteRunMessage(w http.ResponseWriter, r *http.Request) {
	state, _, ok := s.ownedRunFromRequest(w, r)
	if !ok {
		return
	}
	snapshot, err := state.CancelQueueMessage(pathParam(r, "messageId"))
	if err != nil {
		writeRunQueueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func writeRunQueueError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrRunQueueVersionConflict),
		errors.Is(err, ErrRunQueueClosed),
		errors.Is(err, ErrRunQueueItemNotPending),
		errors.Is(err, ErrRunQueueIdempotencyConflict):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrRunQueueItemNotFound):
		writeErr(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrRunQueueFull), strings.Contains(err.Error(), "required"),
		strings.Contains(err.Error(), "unsupported"), strings.Contains(err.Error(), "order"):
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}
