package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
)

type watchlistUpsertBody struct {
	Items []WatchlistItem `json:"items"`
}

type watchlistOrderBody struct {
	Symbols []string `json:"symbols"`
}

func (s *Service) handleListWatchlist(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	items, err := s.Audit.ListWatchlist(r.Context(), accountID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot load watchlist")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) handleUpsertWatchlist(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	var body watchlistUpsertBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	items, err := s.Audit.UpsertWatchlist(r.Context(), accountID, body.Items)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errInvalidWatchlist) {
			status = http.StatusBadRequest
		}
		writeErr(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) handleDeleteWatchlist(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	symbol, err := url.PathUnescape(pathParam(r, "symbol"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid symbol")
		return
	}
	if err := s.Audit.DeleteWatchlistItem(r.Context(), accountID, symbol); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errInvalidWatchlist) {
			status = http.StatusBadRequest
		}
		writeErr(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) handleReorderWatchlist(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	var body watchlistOrderBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	items, err := s.Audit.ReorderWatchlist(r.Context(), accountID, body.Symbols)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errInvalidWatchlist) {
			status = http.StatusBadRequest
		}
		writeErr(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
