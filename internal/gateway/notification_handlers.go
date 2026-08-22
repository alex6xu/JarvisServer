package gateway

import (
	"encoding/json"
	"net/http"
)

type notificationUpsertBody struct {
	Name    string             `json:"name"`
	Enabled bool               `json:"enabled"`
	Events  []string           `json:"events"`
	Config  NotificationConfig `json:"config"`
}

func (s *Service) handleListNotificationChannels(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	channels, err := s.Notifications.List(r.Context(), accountID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot load notification channels")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": channels})
}

func (s *Service) handleUpsertNotificationChannel(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	var body notificationUpsertBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	channel, err := s.Notifications.Upsert(r.Context(), accountID, pathParam(r, "kind"), body.Name, body.Enabled, body.Events, body.Config)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channel": channel})
}

func (s *Service) handleDeleteNotificationChannel(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	if err := s.Notifications.Delete(r.Context(), accountID, pathParam(r, "kind")); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot delete notification channel")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) handleTestNotificationChannel(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	err := s.Notifications.Test(r.Context(), accountID, pathParam(r, "kind"))
	if err != nil {
		if isMissingNotificationChannel(err) {
			writeErr(w, http.StatusNotFound, "notification channel not found")
			return
		}
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}
