package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Service) handleCryptoCandles(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requestAccountID(r); !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	if s.Crypto == nil {
		writeErr(w, http.StatusServiceUnavailable, "crypto market service is unavailable")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	result, err := s.Crypto.Candles(
		r.Context(), r.URL.Query().Get("exchange"), r.URL.Query().Get("symbol"), r.URL.Query().Get("interval"), limit,
	)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, errInvalidCryptoRequest) {
			status = http.StatusBadRequest
		}
		writeErr(w, status, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) handleCryptoStream(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requestAccountID(r); !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	events, err := s.Crypto.Stream(r.Context(), strings.Split(r.URL.Query().Get("symbols"), ","))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errInvalidCryptoRequest) {
			status = http.StatusBadRequest
		}
		writeErr(w, status, err.Error())
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	flush := time.NewTicker(750 * time.Millisecond)
	heartbeat := time.NewTicker(15 * time.Second)
	defer flush.Stop()
	defer heartbeat.Stop()
	pending := make(map[string]CryptoStreamEvent)

	writeEvent := func(event CryptoStreamEvent) bool {
		payload, marshalErr := json.Marshal(event)
		if marshalErr != nil {
			return true
		}
		if _, writeErr := fmt.Fprintf(w, "data: %s\n\n", payload); writeErr != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-events:
			if !open {
				return
			}
			if event.Type == "ticker" && event.Ticker != nil {
				pending[event.Exchange+":"+event.Ticker.Symbol] = event
				continue
			}
			if !writeEvent(event) {
				return
			}
		case <-flush.C:
			for key, event := range pending {
				if !writeEvent(event) {
					return
				}
				delete(pending, key)
			}
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
